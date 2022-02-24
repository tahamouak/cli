package status

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/ghinstance"
	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type StatusOptions struct {
	BaseRepo        func() (ghrepo.Interface, error)
	HttpClient      func() (*http.Client, error)
	HasRepoOverride bool
	Org             string
}

func NewCmdStatus(f *cmdutil.Factory, runF func(*StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{}
	opts.HttpClient = f.HttpClient
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print information about relevant issues, pull requests, and notifications across repositories",
		Long:  "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			// comically, I think this has use scoped to a single repository
			// support `-R, --repo` override
			opts.BaseRepo = f.BaseRepo
			opts.HasRepoOverride = cmd.Flags().Changed("repo")

			if runF != nil {
				return runF(opts)
			}

			return statusRun(opts)
		},
	}

	return cmd
}

type Notification struct {
	Reason  string
	Subject struct {
		Title            string
		LatestCommentURL string `json:"latest_comment_url"`
		URL              string
		Type             string
	}
	Repository struct {
		FullName string `json:"full_name"`
	}
}

type StatusItem struct {
	Repository string // owner/repo
	Identifier string // eg cli/cli#1234
	Preview    string // eg This is the truncated body of a comment...
}

func getNotifications(client *http.Client) ([]Notification, error) {
	apiClient := api.NewClientFromHTTP(client)
	query := url.Values{}
	query.Add("per_page", "100")

	// TODO put Now in opts
	day, _ := time.ParseDuration("24h")
	query.Add("since", time.Now().Add(-day).Format(time.RFC3339))

	// TODO might want to get multiple pages since I'm sorting the results into buckets
	var ret []Notification
	var resp []Notification
	pages := 2
	for page := 1; page <= pages; page++ {
		query.Add("page", fmt.Sprintf("%d", page))
		p := fmt.Sprintf("notifications?%s", query.Encode())
		// behavior when only one page?
		err := apiClient.REST(ghinstance.Default(), "GET", p, nil, &resp)
		if err != nil {
			return nil, fmt.Errorf("could not get notifications: %w", err)
		}
		ret = append(ret, resp...)
	}

	return ret, nil
}

func actualMention(client *http.Client, n Notification) (bool, error) {
	c := api.NewClientFromHTTP(client)
	currentUsername, err := api.CurrentLoginName(c, ghinstance.Default())
	if err != nil {
		return false, err
	}
	resp := struct {
		Body string
	}{}
	if err := c.REST(ghinstance.Default(), "GET", n.Subject.LatestCommentURL, nil, &resp); err != nil {
		return false, err
	}

	return strings.Contains(resp.Body, "@"+currentUsername), nil
}

type IssueOrPR struct {
	Number int
	Title  string
}

type Event struct {
	Type string
	Repo struct {
		Name string // owner/repo
	}
	Payload struct {
		Action      string
		Issue       IssueOrPR
		PullRequest IssueOrPR `json:"pull_request"`
		Comment     struct {
			Body string
		}
	}
}

func getEvents(client *http.Client) ([]Event, error) {
	apiClient := api.NewClientFromHTTP(client)
	query := url.Values{}
	query.Add("per_page", "100")

	currentUsername, err := api.CurrentLoginName(apiClient, ghinstance.Default())
	if err != nil {
		return nil, err
	}

	var ret []Event
	var resp []Event
	pages := 3
	for page := 1; page <= pages; page++ {
		query.Add("page", fmt.Sprintf("%d", page))
		p := fmt.Sprintf("users/%s/events?%s", currentUsername, query.Encode())
		// TODO handle fewer pages (ie page up not down)
		err := apiClient.REST(ghinstance.Default(), "GET", p, nil, &resp)
		if err != nil {
			return nil, fmt.Errorf("could not get events: %w", err)
		}
		ret = append(ret, resp...)
	}

	return ret, nil
}

func statusRun(opts *StatusOptions) error {
	// INITIAL SECTIONS:
	// review requests
	// mentions
	// assigned issues
	// assigned PRs
	// repo activity
	// new issue
	// new pr
	// comment

	// TODO
	// - decide if assignments should come from events or search

	client, err := opts.HttpClient()
	if err != nil {
		return fmt.Errorf("could not create client: %w", err)
	}
	ns, err := getNotifications(client)
	if err != nil {
		return err
	}

	mentions := []StatusItem{}

	for _, n := range ns {
		if n.Reason != "mention" {
			continue
		}

		// TODO error handling
		if actual, err := actualMention(client, n); actual && err != nil {
			mentions = append(mentions, StatusItem{
				Repository: n.Repository.FullName,
				Identifier: "TODO",
				Preview:    "TODO",
			})
		}
	}

	fmt.Println("MENTIONS")
	for _, n := range mentions {
		fmt.Printf("%s %s %s\n", n.Repository, n.Identifier, n.Preview)
	}

	es, err := getEvents(client)
	if err != nil {
		return err
	}

	assignedIssues := []StatusItem{}
	assignedPRs := []StatusItem{}
	reviewRequests := []StatusItem{}
	newIssues := []StatusItem{}
	newPRs := []StatusItem{}
	comments := []StatusItem{}

	for _, e := range es {
		switch e.Type {
		case "IssuesEvent":
			switch e.Payload.Action {
			case "opened":
				newIssues = append(newIssues, StatusItem{
					Identifier: fmt.Sprintf("%d", e.Payload.Issue.Number),
					Repository: e.Repo.Name,
					Preview:    e.Payload.Issue.Title,
				})
			case "assigned":
				assignedIssues = append(assignedIssues, StatusItem{
					Identifier: fmt.Sprintf("%d", e.Payload.Issue.Number),
					Repository: e.Repo.Name,
					Preview:    e.Payload.Issue.Title,
				})
			}
		case "PullRequestEvent":
			switch e.Payload.Action {
			case "opened":
				if e.Payload.PullRequest.Title == "" {
					fmt.Printf("DBG %#v\n", e)
				}
				newPRs = append(newPRs, StatusItem{
					Identifier: fmt.Sprintf("%d", e.Payload.PullRequest.Number),
					Repository: e.Repo.Name,
					Preview:    e.Payload.PullRequest.Title,
				})
			case "review_requested":
				reviewRequests = append(reviewRequests, StatusItem{
					Identifier: fmt.Sprintf("%d", e.Payload.PullRequest.Number),
					Repository: e.Repo.Name,
					Preview:    e.Payload.PullRequest.Title,
				})
			case "assigned":
				assignedPRs = append(assignedPRs, StatusItem{
					Identifier: fmt.Sprintf("%d", e.Payload.PullRequest.Number),
					Repository: e.Repo.Name,
					Preview:    e.Payload.PullRequest.Title,
				})
			}
		case "IssueCommentEvent":
			body := e.Payload.Comment.Body
			if len(body) > 20 {
				body = body[0:20]
			}
			comments = append(comments, StatusItem{
				Identifier: fmt.Sprintf("%d", e.Payload.Issue.Number),
				Repository: e.Repo.Name,
				Preview:    body,
			})
		}
	}

	fmt.Println("NEW ISSUES")
	fmt.Printf("DBG %#v\n", newIssues)

	fmt.Println("NEW PRs")
	fmt.Printf("DBG %#v\n", newPRs)

	fmt.Println("REVIEW REQUESTS")
	fmt.Printf("DBG %#v\n", reviewRequests)

	fmt.Println("ASSIGNED PRs")
	fmt.Printf("DBG %#v\n", assignedPRs)

	fmt.Println("ASSIGNED ISSUES")
	fmt.Printf("DBG %#v\n", assignedIssues)

	fmt.Println("COMMENTS")
	fmt.Printf("DBG %#v\n", comments)

	// TODO
	// - first pass on formatting
	// - switch to search API for assignments
	// - goroutines for each network call + subsequent processing
	// - ensure caching appropriately

	return nil
}
