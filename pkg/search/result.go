package search

import (
	"reflect"
	"strings"
	"time"
)

var RepositoryFields = []string{
	"createdAt",
	"defaultBranch",
	"description",
	"forksCount",
	"fullName",
	"hasDownloads",
	"hasIssues",
	"hasPages",
	"hasProjects",
	"hasWiki",
	"homepage",
	"id",
	"isArchived",
	"isDisabled",
	"isFork",
	"isPrivate",
	"language",
	"license",
	"name",
	"openIssuesCount",
	"owner",
	"pushedAt",
	"size",
	"stargazersCount",
	"updatedAt",
	"visibility",
	"watchersCount",
}

var IssueFields = []string{
	"assignee",
	"authorAssociation",
	"body",
	"closedAt",
	"comments",
	"createdAt",
	"id",
	"labels",
	"isLocked",
	"number",
	"pullRequestLinks",
	"state",
	"title",
	"updatedAt",
}

type RepositoriesResult struct {
	IncompleteResults bool         `json:"incomplete_results"`
	Items             []Repository `json:"items"`
	Total             int          `json:"total_count"`
}

type IssuesResult struct {
	IncompleteResults bool    `json:"incomplete_results"`
	Items             []Issue `json:"items"`
	Total             int     `json:"total_count"`
}

type Repository struct {
	CreatedAt       time.Time `json:"created_at"`
	DefaultBranch   string    `json:"default_branch"`
	Description     string    `json:"description"`
	ForksCount      int       `json:"forks_count"`
	FullName        string    `json:"full_name"`
	HasDownloads    bool      `json:"has_downloads"`
	HasIssues       bool      `json:"has_issues"`
	HasPages        bool      `json:"has_pages"`
	HasProjects     bool      `json:"has_projects"`
	HasWiki         bool      `json:"has_wiki"`
	Homepage        string    `json:"homepage"`
	ID              int64     `json:"id"`
	IsArchived      bool      `json:"archived"`
	IsDisabled      bool      `json:"disabled"`
	IsFork          bool      `json:"fork"`
	IsPrivate       bool      `json:"private"`
	Language        string    `json:"language"`
	License         License   `json:"license"`
	MasterBranch    string    `json:"master_branch"`
	Name            string    `json:"name"`
	OpenIssuesCount int       `json:"open_issues_count"`
	Owner           User      `json:"owner"`
	PushedAt        time.Time `json:"pushed_at"`
	Size            int       `json:"size"`
	StargazersCount int       `json:"stargazers_count"`
	UpdatedAt       time.Time `json:"updated_at"`
	Visibility      string    `json:"visibility"`
	WatchersCount   int       `json:"watchers_count"`
}

type License struct {
	HTMLURL string `json:"html_url"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	URL     string `json:"url"`
}

type User struct {
	GravatarID string `json:"gravatar_id"`
	ID         int64  `json:"id"`
	Login      string `json:"login"`
	SiteAdmin  bool   `json:"site_admin"`
	Type       string `json:"type"`
}

type Issue struct {
	Assignee          User             `json:"assignee"`
	AuthorAssociation string           `json:"author_association"`
	Body              string           `json:"body"`
	ClosedAt          time.Time        `json:"closed_at"`
	Comments          int              `json:"comments"`
	CreatedAt         time.Time        `json:"created_at"`
	HTMLURL           string           `json:"html_url"`
	ID                int64            `json:"id"`
	Labels            []Label          `json:"labels"`
	IsLocked          bool             `json:"locked"`
	Number            int              `json:"number"`
	PullRequestLinks  PullRequestLinks `json:"pull_request"`
	RepositoryURL     string           `json:"repository_url"`
	State             string           `json:"state"`
	Title             string           `json:"title"`
	URL               string           `json:"url"`
	UpdatedAt         time.Time        `json:"updated_at"`
	User              User             `json:"user"`
}

type PullRequestLinks struct {
	DiffURL  string `json:"diff_url"`
	HTMLURL  string `json:"html_url"`
	PatchURL string `json:"patch_url"`
	URL      string `json:"url"`
}

type Label struct {
	Color string `json:"color"`
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

func (repo Repository) ExportData(fields []string) map[string]interface{} {
	v := reflect.ValueOf(repo)
	data := map[string]interface{}{}
	for _, f := range fields {
		switch f {
		case "license":
			data[f] = map[string]interface{}{
				"key":  repo.License.Key,
				"name": repo.License.Name,
				"url":  repo.License.URL,
			}
		case "owner":
			data[f] = map[string]interface{}{
				"id":    repo.Owner.ID,
				"login": repo.Owner.Login,
				"type":  repo.Owner.Type,
			}
		default:
			sf := fieldByName(v, f)
			data[f] = sf.Interface()
		}
	}
	return data
}

func (issue Issue) IsPullRequest() bool {
	return issue.PullRequestLinks.URL != ""
}

func (issue Issue) ExportData(fields []string) map[string]interface{} {
	v := reflect.ValueOf(issue)
	data := map[string]interface{}{}
	for _, f := range fields {
		switch f {
		case "assignee":
			data[f] = map[string]interface{}{
				"id":    issue.Assignee.ID,
				"login": issue.Assignee.Login,
				"type":  issue.Assignee.Type,
			}
		case "Labels":
			labels := make([]interface{}, 0, len(issue.Labels))
			for _, label := range issue.Labels {
				labels = append(labels, map[string]interface{}{
					"color": label.Color,
					"id":    label.ID,
					"name":  label.Name,
					"url":   label.URL,
				})
			}
			data[f] = labels
		case "PullRequestLinks":
			data[f] = map[string]interface{}{
				"diffUrl":  issue.PullRequestLinks.DiffURL,
				"htmlUrl":  issue.PullRequestLinks.HTMLURL,
				"patchUrl": issue.PullRequestLinks.PatchURL,
				"url":      issue.PullRequestLinks.URL,
			}
		case "User":
			data[f] = map[string]interface{}{
				"id":    issue.User.ID,
				"login": issue.User.Login,
				"type":  issue.User.Type,
			}
		default:
			sf := fieldByName(v, f)
			data[f] = sf.Interface()
		}
	}
	return data
}

func fieldByName(v reflect.Value, field string) reflect.Value {
	return v.FieldByNameFunc(func(s string) bool {
		return strings.EqualFold(field, s)
	})
}
