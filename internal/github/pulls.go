package github

import (
	"context"
	"fmt"
	"strings"
)

// PR represents a GitHub pull request.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"html_url"`
	NodeID string `json:"node_id"`
}

// CreatePRRequest holds the fields needed to open a pull request.
type CreatePRRequest struct {
	Title string
	Body  string
	Head  string // source branch
	Base  string // target branch (e.g. "main")
	Draft bool
}

// GetDefaultBranchSHA returns the commit SHA at the tip of the repo's
// default branch. Used as the base when creating a new worker branch.
func (c *Client) GetDefaultBranchSHA(ctx context.Context, owner, repo, branch string) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", owner, repo, branch)

	var result struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.do(ctx, path, &result); err != nil {
		return "", fmt.Errorf("get branch SHA %s/%s@%s: %w", owner, repo, branch, err)
	}
	if result.Object.SHA == "" {
		return "", fmt.Errorf("empty SHA returned for %s/%s@%s", owner, repo, branch)
	}
	return result.Object.SHA, nil
}

// BranchExists reports whether branch already exists in owner/repo.
// Returns (false, nil) when the branch is not found; propagates other errors.
func (c *Client) BranchExists(ctx context.Context, owner, repo, branch string) (bool, error) {
	path := fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", owner, repo, branch)
	if err := c.do(ctx, path, nil); err != nil {
		if strings.Contains(err.Error(), " 404 ") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateBranch creates a new branch in owner/repo pointing at baseSHA.
func (c *Client) CreateBranch(ctx context.Context, owner, repo, branch, baseSHA string) error {
	path := fmt.Sprintf("/repos/%s/%s/git/refs", owner, repo)
	body := map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": baseSHA,
	}
	if err := c.doMethod(ctx, "POST", path, body, nil); err != nil {
		return fmt.Errorf("create branch %s in %s/%s: %w", branch, owner, repo, err)
	}
	return nil
}

// CreatePR opens a pull request in owner/repo and returns the created PR.
func (c *Client) CreatePR(ctx context.Context, owner, repo string, req CreatePRRequest) (*PR, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls", owner, repo)
	body := map[string]any{
		"title": req.Title,
		"body":  req.Body,
		"head":  req.Head,
		"base":  req.Base,
		"draft": req.Draft,
	}
	var pr PR
	if err := c.doMethod(ctx, "POST", path, body, &pr); err != nil {
		return nil, fmt.Errorf("create PR in %s/%s: %w", owner, repo, err)
	}
	return &pr, nil
}

// MergePR squash-merges pull request number in owner/repo.
func (c *Client) MergePR(ctx context.Context, owner, repo string, number int, commitTitle string) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", owner, repo, number)
	body := map[string]string{
		"merge_method": "squash",
		"commit_title": commitTitle,
	}
	if err := c.doMethod(ctx, "PUT", path, body, nil); err != nil {
		return fmt.Errorf("merge PR #%d in %s/%s: %w", number, owner, repo, err)
	}
	return nil
}

// PostComment posts a comment on an issue or PR (GitHub uses the same endpoint).
func (c *Client) PostComment(ctx context.Context, owner, repo string, number int, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := c.doMethod(ctx, "POST", path, map[string]string{"body": body}, nil); err != nil {
		return fmt.Errorf("post comment on %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}

// PRDetail holds the title and body of a pull request, used by review agents.
type PRDetail struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// GetPR fetches the title and body of a pull request.
func (c *Client) GetPR(ctx context.Context, owner, repo string, number int) (*PRDetail, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	var pr PRDetail
	if err := c.do(ctx, path, &pr); err != nil {
		return nil, fmt.Errorf("get PR %s/%s#%d: %w", owner, repo, number, err)
	}
	return &pr, nil
}

// PRFile is a file changed by a pull request.
type PRFile struct {
	Filename string `json:"filename"`
	Status   string `json:"status"` // "added", "modified", "removed", "renamed"
}

// GetPRFiles returns the files changed by a pull request.
func (c *Client) GetPRFiles(ctx context.Context, owner, repo string, number int) ([]PRFile, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files", owner, repo, number)
	var files []PRFile
	if err := c.do(ctx, path, &files); err != nil {
		return nil, fmt.Errorf("get files for PR %s/%s#%d: %w", owner, repo, number, err)
	}
	return files, nil
}

// CloseIssue closes an issue in owner/repo.
func (c *Client) CloseIssue(ctx context.Context, owner, repo string, number int) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.doMethod(ctx, "PATCH", path, map[string]string{"state": "closed"}, nil); err != nil {
		return fmt.Errorf("close issue %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}
