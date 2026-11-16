// Package pipeline runs the post-scraping analysis of project runs: classify every post
// with Claude, then build the xlsx exports and upload them to S3.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/analysis"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/export"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/storage"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

const xlsxContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

type Analyzer struct {
	Store       *store.Store
	Files       *storage.S3
	Classifier  *analysis.Classifier
	Log         *slog.Logger
	Concurrency int
	// Follow-up comments collection for negative posts (0 = off)
	CommentsPerRun  int
	CommentsPerPost int
}

// CompanyFromProject is what the classifier knows about a project.
func CompanyFromProject(p store.Project) analysis.Company {
	return analysis.Company{
		Name: p.Name, Market: p.Market, Aliases: p.Aliases,
		OfficialAccounts: p.OfficialAccounts, Competitors: p.Competitors, Notes: p.Notes,
	}
}

// Run processes pending analyses until ctx is done. Only one Analyzer per backend.
func (a *Analyzer) Run(ctx context.Context) {
	if n, err := a.Store.ResetRunningAnalyses(ctx); err != nil {
		a.Log.Error("reset running analyses", "err", err)
	} else if n > 0 {
		a.Log.Warn("resumed interrupted analyses", "count", n)
	}
	for ctx.Err() == nil {
		job, err := a.Store.ClaimAnalysis(ctx)
		if errors.Is(err, store.ErrNotFound) {
			sleep(ctx, 3*time.Second)
			continue
		}
		if err != nil {
			a.Log.Error("claim analysis", "err", err)
			sleep(ctx, 10*time.Second)
			continue
		}
		a.process(ctx, job.ID)
	}
}

func (a *Analyzer) process(ctx context.Context, jobID int64) {
	start := time.Now()
	job, err := a.Store.GetJobByID(ctx, jobID)
	if err != nil {
		a.Log.Error("load job for analysis", "job", jobID, "err", err)
		return
	}
	if job.ProjectID == nil {
		_ = a.Store.FailAnalysis(ctx, jobID, "Проект был удалён", "")
		return
	}
	project, err := a.Store.GetProjectByID(ctx, *job.ProjectID)
	if err != nil {
		_ = a.Store.FailAnalysis(ctx, jobID, "Проект не найден", "")
		return
	}
	company := CompanyFromProject(project)
	version := analysis.PromptVersion(company)

	posts, err := a.Store.PostsToLabel(ctx, jobID, project.ID, version)
	if err != nil {
		a.Log.Error("posts to label", "job", jobID, "err", err)
		_ = a.Store.FailAnalysis(ctx, jobID, "Внутренняя ошибка при анализе", "")
		return
	}

	failed, usage := a.classify(ctx, project.ID, company, version, posts)
	if ctx.Err() != nil {
		return // shutting down: the job stays "running" and is resumed on the next start
	}

	if job.Kind == store.JobKindComments {
		// Comments are shown under their posts on the site; no files of their own
		if failed > 0 {
			_ = a.Store.FailAnalysis(ctx, jobID, fmt.Sprintf("Не удалось проанализировать %d комментариев", failed), "")
		} else if err := a.Store.FinishAnalysis(ctx, jobID, "", ""); err != nil {
			a.Log.Error("finish comments analysis", "job", jobID, "err", err)
		}
		a.Log.Info("comments analysis done", "job", jobID, "classified", len(posts)-failed, "failed", failed)
		a.refreshParentFiles(ctx, job)
		return
	}

	mainKey, removedKey, buildErr := a.buildFiles(ctx, job)
	switch {
	case buildErr != nil:
		a.Log.Error("build analysis files", "job", jobID, "err", buildErr)
		_ = a.Store.FailAnalysis(ctx, jobID, "Не удалось сохранить файл с результатами", "")
	case failed > 0:
		_ = a.Store.FailAnalysis(ctx, jobID,
			fmt.Sprintf("Не удалось проанализировать %d из %d постов — их тональность не определена", failed, len(posts)), mainKey)
	default:
		if err := a.Store.FinishAnalysis(ctx, jobID, mainKey, removedKey); err != nil {
			a.Log.Error("finish analysis", "job", jobID, "err", err)
		}
	}
	a.queueComments(ctx, job, project.ID)
	a.Log.Info("analysis done", "job", jobID, "project", project.ID, "classified", len(posts)-failed, "failed", failed,
		"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "cache_read", usage.CacheReadTokens,
		"ms", time.Since(start).Milliseconds())
}

func (a *Analyzer) classify(ctx context.Context, projectID int64, company analysis.Company, version string, posts []store.Post) (failed int, total analysis.Usage) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(1, a.Concurrency))
	for _, p := range posts {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p store.Post) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			defer cancel()
			label, usage, err := a.Classifier.Classify(cctx, company,
				analysis.Post{ID: p.ID, AuthorUsername: p.AuthorUsername, Content: p.Content, Context: p.Context})
			if err == nil {
				err = a.Store.SaveLabel(ctx, projectID, p.ID, store.PostLabel{
					Category: label.Category, Market: label.Market, Sentiment: label.Sentiment, Aspects: label.Aspects,
					Language: label.Language, Reason: label.Reason, Excluded: label.Excluded(),
				}, a.Classifier.Model(), version)
			}
			mu.Lock()
			defer mu.Unlock()
			total.Add(usage)
			if err != nil {
				failed++
				a.Log.Warn("classify post", "post", p.ID, "err", err)
			}
		}(p)
	}
	wg.Wait()
	return failed, total
}

// refreshParentFiles rebuilds the run's xlsx files so they include the comments just analyzed.
func (a *Analyzer) refreshParentFiles(ctx context.Context, commentsJob store.Job) {
	if commentsJob.ParentJobID == nil {
		return
	}
	parent, err := a.Store.GetJobByID(ctx, *commentsJob.ParentJobID)
	if err != nil {
		a.Log.Error("load parent job", "job", *commentsJob.ParentJobID, "err", err)
		return
	}
	mainKey, removedKey, err := a.buildFiles(ctx, parent)
	if err == nil {
		err = a.Store.SetJobFiles(ctx, parent.ID, mainKey, removedKey)
	}
	if err != nil {
		a.Log.Error("rebuild files with comments", "job", parent.ID, "err", err)
		return
	}
	a.Log.Info("files rebuilt with comments", "job", parent.ID)
}

// queueComments asks the worker to collect comments under the most discussed negative posts.
func (a *Analyzer) queueComments(ctx context.Context, job store.Job, projectID int64) {
	if a.CommentsPerRun <= 0 {
		return
	}
	urls, err := a.Store.PostsForComments(ctx, job.ID, projectID, a.CommentsPerRun)
	if err != nil {
		a.Log.Error("posts for comments", "job", job.ID, "err", err)
		return
	}
	if len(urls) == 0 {
		return
	}
	c, err := a.Store.CreateCommentsJob(ctx, job, urls, a.CommentsPerPost)
	if err != nil {
		a.Log.Error("create comments job", "job", job.ID, "err", err)
		return
	}
	a.Log.Info("comments job queued", "job", job.ID, "comments_job", c.ID, "posts", len(urls))
}

func (a *Analyzer) buildFiles(ctx context.Context, job store.Job) (mainKey, removedKey string, err error) {
	posts, err := a.Store.JobLabeledPosts(ctx, job.ID)
	if err != nil {
		return "", "", err
	}
	comments, err := a.Store.JobComments(ctx, job.ID)
	if err != nil {
		return "", "", err
	}
	main, removed, err := export.BuildLabeledXLSX(posts, comments, job.IsFirstRun)
	if err != nil {
		return "", "", err
	}
	prefix := fmt.Sprintf("users/%d/jobs/%d/", job.UserID, job.ID)
	mainKey = prefix + "posts.xlsx"
	if err := a.Files.Put(ctx, mainKey, xlsxContentType, main); err != nil {
		return "", "", err
	}
	if removed != nil {
		removedKey = prefix + "removed.xlsx"
		if err := a.Files.Put(ctx, removedKey, xlsxContentType, removed); err != nil {
			return "", "", err
		}
	}
	return mainKey, removedKey, nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
