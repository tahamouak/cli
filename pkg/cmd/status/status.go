package status

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/ghinstance"
	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/cli/cli/v2/utils"
	"github.com/spf13/cobra"
)

type StatusOptions struct {
	BaseRepo        func() (ghrepo.Interface, error)
	HttpClient      func() (*http.Client, error)
	HasRepoOverride bool
	Org             string
	IO              iostreams.IOStreams
}

func NewCmdStatus(f *cmdutil.Factory, runF func(*StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{}
	opts.HttpClient = f.HttpClient
	opts.IO = *f.IOStreams
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
	preview    string // eg This is the truncated body of a comment...
	Reason     string // only used in repo activity
}

func (s StatusItem) Preview() string {
	return strings.ReplaceAll(strings.ReplaceAll(s.preview, "\r", ""), "\n", "")
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

func actualMention(client *http.Client, n Notification) (string, error) {
	c := api.NewClientFromHTTP(client)
	currentUsername, err := api.CurrentLoginName(c, ghinstance.Default())
	if err != nil {
		return "", err
	}
	resp := struct {
		Body string
	}{}
	if err := c.REST(ghinstance.Default(), "GET", n.Subject.LatestCommentURL, nil, &resp); err != nil {
		return "", err
	}

	var ret string

	if strings.Contains(resp.Body, "@"+currentUsername) {
		ret = resp.Body
	}

	return ret, nil
}

type IssueOrPR struct {
	Number int
	Title  string
}

type Event struct {
	Type      string
	CreatedAt time.Time `json:"created_at"`
	Repo      struct {
		Name string // owner/repo
	}
	Payload struct {
		Action      string
		Issue       IssueOrPR
		PullRequest IssueOrPR `json:"pull_request"`
		Comment     struct {
			Body    string
			HTMLURL string `json:"html_url"`
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

type SearchResult struct {
	Type       string `json:"__typename"`
	UpdatedAt  time.Time
	Title      string
	Number     int
	Repository struct {
		NameWithOwner string
	}
}

type SearchResults struct {
	AssignedPRs    []SearchResult
	AssignedIssues []SearchResult
	ReviewRequests []SearchResult
}

func doSearch(client *http.Client) (*SearchResults, error) {
	q := `
	query AssignedSearch {
	  assignments: search(first: 25, type: ISSUE, query:"assignee:@me state:open") {
		  edges {
		  node {
			...on Issue {
			  __typename
			  updatedAt
			  title
			  number
			  repository {
				nameWithOwner
			  }
			}
			...on PullRequest {
			  updatedAt
			  __typename
			  title
			  number
			  repository {
				nameWithOwner
			  }
			}
		  }
		}
	  }
	  reviewRequested: search(first: 25, type: ISSUE, query:"state:open review-requested:@me") {
		  edges {
			  node {
				...on PullRequest {
				  updatedAt
				  __typename
				  title
				  number
				  repository {
					nameWithOwner
				  }
				}
			  }
		  }
	  }
	}`
	apiClient := api.NewClientFromHTTP(client)

	var resp struct {
		Assignments struct {
			Edges []struct {
				Node SearchResult
			}
		}
		ReviewRequested struct {
			Edges []struct {
				Node SearchResult
			}
		}
	}
	err := apiClient.GraphQL(ghinstance.Default(), q, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("could not search for assignments: %w", err)
	}

	prs := []SearchResult{}
	issues := []SearchResult{}
	reviewRequested := []SearchResult{}

	for _, e := range resp.Assignments.Edges {
		if e.Node.Type == "Issue" {
			issues = append(issues, e.Node)
		} else if e.Node.Type == "PullRequest" {
			prs = append(prs, e.Node)
		} else {
			panic("you shouldn't be here")
		}
	}

	for _, e := range resp.ReviewRequested.Edges {
		reviewRequested = append(reviewRequested, e.Node)
	}

	sort.Sort(Results(issues))
	sort.Sort(Results(prs))
	sort.Sort(Results(reviewRequested))

	// TODO convert to Status Items

	return &SearchResults{
		AssignedIssues: issues,
		AssignedPRs:    prs,
		ReviewRequests: reviewRequested,
	}, nil

}

type Results []SearchResult

func (rs Results) Len() int {
	return len(rs)
}

func (rs Results) Less(i, j int) bool {
	return rs[i].UpdatedAt.After(rs[j].UpdatedAt)
}

func (rs Results) Swap(i, j int) {
	rs[i], rs[j] = rs[j], rs[i]
}

func statusRun(opts *StatusOptions) error {
	// INITIAL SECTIONS:
	// assigned issues
	// assigned PRs
	// review requests
	// mentions
	// repo activity
	// 	new issue
	// 	new pr
	// 	comment

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

		if actual, err := actualMention(client, n); actual != "" && err == nil {
			split := strings.Split(n.Subject.URL, "/")
			mentions = append(mentions, StatusItem{
				Repository: n.Repository.FullName,
				Identifier: split[len(split)-1],
				preview:    actual,
			})
		} else if err != nil {
			return fmt.Errorf("could not fetch comment: %w", err)
		}
	}

	es, err := getEvents(client)
	if err != nil {
		return err
	}

	repoActivity := []StatusItem{}

	for _, e := range es {
		switch e.Type {
		case "IssuesEvent":
			if e.Payload.Action != "opened" {
				continue
			}
			repoActivity = append(repoActivity, StatusItem{
				Identifier: fmt.Sprintf("%d", e.Payload.Issue.Number),
				Repository: e.Repo.Name,
				preview:    e.Payload.Issue.Title,
				Reason:     "new issue",
			})
		case "PullRequestEvent":
			if e.Payload.Action != "opened" {
				continue
			}
			repoActivity = append(repoActivity, StatusItem{
				Identifier: fmt.Sprintf("%d", e.Payload.PullRequest.Number),
				Repository: e.Repo.Name,
				preview:    e.Payload.PullRequest.Title,
				Reason:     "new PR",
			})
		case "IssueCommentEvent":
			reason := "issue comment"
			// I'm so sorry
			if strings.Contains(e.Payload.Comment.HTMLURL, `/pull/`) {
				reason = "PR comment"
			}
			repoActivity = append(repoActivity, StatusItem{
				Identifier: fmt.Sprintf("%d", e.Payload.Issue.Number),
				Repository: e.Repo.Name,
				preview:    e.Payload.Comment.Body,
				Reason:     reason,
			})
		}
	}

	results, err := doSearch(client)
	if err != nil {
		return err
	}

	cs := opts.IO.ColorScheme()
	out := opts.IO.Out

	halfWidth := (opts.IO.TerminalWidth() / 2) - 2

	idStyle := cs.Cyan
	headerStyle := cs.Bold
	halfStyle := lipgloss.NewStyle().Width(halfWidth).Padding(0)
	maxLen := 5

	// TODO rename to renderSection; take a list of status items
	// TODO use this for mentions once above is done
	renderSearchResults := func(header string, results []SearchResult) string {
		tableOut := &bytes.Buffer{}
		fmt.Fprintln(tableOut, headerStyle(header))
		tp := utils.NewTablePrinterWithOptions(&opts.IO, utils.TablePrinterOptions{
			IsTTY:    opts.IO.IsStdoutTTY(),
			MaxWidth: halfWidth,
			Out:      tableOut,
		})
		if len(results) == 0 {
			tp.AddField("Nothing here ^_^", nil, nil)
			tp.EndRow()
		} else {
			for i, r := range results {
				if i == maxLen {
					break
				}
				tp.AddField(
					fmt.Sprintf("%s#%d", r.Repository.NameWithOwner, r.Number),
					nil, idStyle)
				tp.AddField(r.Title, nil, nil)
				tp.EndRow()
			}
		}

		tp.Render()

		return tableOut.String()
	}

	prSection := renderSearchResults("Assigned PRs", results.AssignedPRs)
	issuesSection := renderSearchResults("Assigned Issues", results.AssignedIssues)
	reviewSection := renderSearchResults("Review Requests", results.ReviewRequests)

	mOut := &bytes.Buffer{}
	fmt.Fprintln(mOut, headerStyle("Mentions"))
	mTP := utils.NewTablePrinterWithOptions(&opts.IO, utils.TablePrinterOptions{
		IsTTY:    opts.IO.IsStdoutTTY(),
		MaxWidth: halfWidth,
		Out:      mOut,
	})

	if len(mentions) > 0 {
		for i, m := range mentions {
			if i == maxLen {
				break
			}
			mTP.AddField(
				fmt.Sprintf("%s#%s", m.Repository, m.Identifier),
				nil, idStyle)
			mTP.AddField(m.Preview(), nil, nil)
			mTP.EndRow()
		}
	} else {
		mTP.AddField("Nothing here ^_^", nil, nil)
	}

	mTP.Render()

	mentionsP := halfStyle.Render(mOut.String())
	reviewRequestsP := halfStyle.Render(reviewSection)
	assignedPRsP := halfStyle.Render(prSection)
	assignedIssuesP := halfStyle.Render(issuesSection)

	fmt.Fprintln(out, lipgloss.JoinHorizontal(lipgloss.Top, assignedIssuesP, assignedPRsP))
	fmt.Fprintln(out, lipgloss.JoinHorizontal(lipgloss.Top, reviewRequestsP, mentionsP))

	// TODO
	// - evaluate formatting/lipgloss use
	// - go/no-go on greeting
	// - goroutines for each network call + subsequent processing
	// - ensure caching appropriately
	// - do a version of this where lipgloss is only used for horizontal alignment
	// - do a version without lipgloss

	fmt.Fprintln(mOut, headerStyle("Mentions"))
	raTP := utils.NewTablePrinter(&opts.IO)

	for i, ra := range repoActivity {
		if i >= 10 {
			break
		}
		raTP.AddField(ra.Repository, nil, nil)
		raTP.AddField(ra.Reason, nil, nil)
		raTP.AddField(ra.Identifier, nil, idStyle)
		raTP.AddField(ra.Preview(), nil, nil)
		raTP.EndRow()
	}

	fmt.Fprintln(out, headerStyle("Repository Activity"))

	raTP.Render()

	return nil
}
