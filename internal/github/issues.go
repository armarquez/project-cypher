package github

import (
	"context"
	"fmt"
)

// Task is a unit of work parsed from a GitHub Issue. This is what the
// orchestrator hands off to a worker session.
type Task struct {
	IssueNumber int
	Title       string
	Body        string
	RepoOwner   string
	RepoName    string
	Labels      []string
	URL         string
}

// ghIssue is the raw GitHub API representation of an issue.
type ghIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ListOpenIssues returns all open issues for owner/repo that carry label.
// Results are ordered oldest-first (creation order) so the orchestrator
// works the queue in FIFO order. Paginates automatically.
func (c *Client) ListOpenIssues(ctx context.Context, owner, repo, label string) ([]Task, error) {
	var all []Task
	for page := 1; ; page++ {
		batch, err := c.fetchIssuePage(ctx, owner, repo, label, page)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
		page++
	}
	return all, nil
}

func (c *Client) fetchIssuePage(ctx context.Context, owner, repo, label string, page int) ([]Task, error) {
	path := fmt.Sprintf(
		"/repos/%s/%s/issues?state=open&labels=%s&sort=created&direction=asc&per_page=100&page=%d",
		owner, repo, label, page,
	)

	var raw []ghIssue
	if err := c.do(ctx, path, &raw); err != nil {
		return nil, fmt.Errorf("list issues (page %d): %w", page, err)
	}

	tasks := make([]Task, len(raw))
	for i, issue := range raw {
		labels := make([]string, len(issue.Labels))
		for j, l := range issue.Labels {
			labels[j] = l.Name
		}
		tasks[i] = Task{
			IssueNumber: issue.Number,
			Title:       issue.Title,
			Body:        issue.Body,
			RepoOwner:   owner,
			RepoName:    repo,
			Labels:      labels,
			URL:         issue.HTMLURL,
		}
	}
	return tasks, nil
}
