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
	// review requests
	// mentions
	// assigned issues
	// assigned PRs
	// repo activity
	// new issue
	// new pr
	// comment

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

	newIssues := []StatusItem{}
	newPRs := []StatusItem{}
	comments := []StatusItem{}

	// TODO cleanup switches
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

	fmt.Println("COMMENTS")
	fmt.Printf("DBG %#v\n", comments)

	results, err := doSearch(client)
	if err != nil {
		return err
	}

	fmt.Println("ASSIGNED PRs")
	fmt.Printf("DBG %#v\n", results.AssignedPRs)

	fmt.Println("ASSIGNED ISSUES")
	fmt.Printf("DBG %#v\n", results.AssignedIssues)

	// TODO
	// - first pass on formatting
	// - goroutines for each network call + subsequent processing
	// - ensure caching appropriately

	out := opts.IO.Out

	titleStyle := lipgloss.NewStyle().Width(opts.IO.TerminalWidth()).
		Align(lipgloss.Center).Bold(true).Underline(true)

	g, err := greeting(client)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, titleStyle.Render(g))
	fmt.Fprintln(out)

	// thoughts on formatting

	// Given enough space, this layout:
	// Assigned PRs     Assigned Issue
	// Review Requests  Mentions
	// Repo Activity

	// Linebreak the top row if not enough space

	// could exploit table printer to just fake multiple tables on same line

	idStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF"))
	nothingStyle := lipgloss.NewStyle().Italic(true)
	headerStyle := lipgloss.NewStyle().Bold(true)
	halfStyle := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).Width(50)
	maxLen := 5

	prOut := &bytes.Buffer{}
	fmt.Fprintln(prOut, headerStyle.Render("Assigned PRs"))
	prTP := utils.NewTablePrinterWithOptions(&opts.IO, utils.TablePrinterOptions{
		IsTTY:    opts.IO.IsStdoutTTY(),
		MaxWidth: 50,
		Out:      prOut,
	})

	for i, pr := range results.AssignedPRs {
		if i == maxLen {
			break
		}
		prTP.AddField(
			fmt.Sprintf("%s#%d", pr.Repository.NameWithOwner, pr.Number),
			nil,
			func(s string) string { return idStyle.Render(s) })
		prTP.AddField(pr.Title, nil, nil)
		prTP.EndRow()
	}

	prTP.Render()

	rrOut := &bytes.Buffer{}
	fmt.Fprintln(rrOut, headerStyle.Render("Review Requests"))

	rrTP := utils.NewTablePrinterWithOptions(&opts.IO, utils.TablePrinterOptions{
		IsTTY:    opts.IO.IsStdoutTTY(),
		MaxWidth: 50,
		Out:      rrOut,
	})

	if len(results.ReviewRequests) > 0 {
		for i, rr := range results.ReviewRequests {
			if i == maxLen {
				break
			}
			rrTP.AddField(
				fmt.Sprintf("%s#%d", rr.Repository.NameWithOwner, rr.Number),
				nil,
				func(s string) string { return idStyle.Render(s) })
			rrTP.AddField(rr.Title, nil, nil)
			rrTP.EndRow()
		}
	} else {
		rrTP.AddField(
			"Nothing here ^_^",
			nil,
			func(s string) string { return nothingStyle.Render(s) },
		)
	}

	rrTP.Render()

	aiOut := &bytes.Buffer{}
	fmt.Fprintln(aiOut, headerStyle.Render("Assigned Issues"))

	aiTP := utils.NewTablePrinterWithOptions(&opts.IO, utils.TablePrinterOptions{
		IsTTY:    opts.IO.IsStdoutTTY(),
		MaxWidth: 50,
		Out:      aiOut,
	})

	if len(results.ReviewRequests) > 0 {
		for i, ai := range results.AssignedIssues {
			if i == maxLen {
				break
			}
			aiTP.AddField(
				fmt.Sprintf("%s#%d", ai.Repository.NameWithOwner, ai.Number),
				nil,
				func(s string) string { return idStyle.Render(s) })
			aiTP.AddField(ai.Title, nil, nil)
			aiTP.EndRow()
		}
	} else {
		aiTP.AddField(
			"Nothing here ^_^",
			nil,
			func(s string) string { return nothingStyle.Render(s) },
		)
	}

	aiTP.Render()

	mOut := &bytes.Buffer{}
	fmt.Fprintln(mOut, headerStyle.Render("Mentions"))
	mTP := utils.NewTablePrinterWithOptions(&opts.IO, utils.TablePrinterOptions{
		IsTTY:    opts.IO.IsStdoutTTY(),
		MaxWidth: 50,
		Out:      mOut,
	})

	if len(mentions) > 0 {
		for i, m := range mentions {
			if i == maxLen {
				break
			}
			mTP.AddField(
				fmt.Sprintf("%s#%s", m.Repository, m.Identifier),
				nil,
				func(s string) string { return idStyle.Render(s) })
			mTP.AddField(m.Preview, nil, nil)
			mTP.EndRow()
		}
	} else {
		mTP.AddField(
			"Nothing here ^_^",
			nil,
			func(s string) string { return nothingStyle.Render(s) },
		)
	}

	mTP.Render()

	mentionsP := halfStyle.Render(mOut.String())
	reviewRequestsP := halfStyle.Render(rrOut.String())
	assignedPRsP := halfStyle.Render(prOut.String())
	assignedIssuesP := halfStyle.Render(aiOut.String())

	fmt.Fprintln(out, lipgloss.JoinHorizontal(lipgloss.Top, assignedIssuesP, assignedPRsP))
	fmt.Fprintln(out, lipgloss.JoinHorizontal(lipgloss.Top, reviewRequestsP, mentionsP))

	return nil
}

func greeting(client *http.Client) (string, error) {
	c := api.NewClientFromHTTP(client)
	currentUsername, err := api.CurrentLoginName(c, ghinstance.Default())
	if err != nil {
		return "", err
	}

	// TODO figure out how to compute time greeting
	//now := time.Now().Local()

	return fmt.Sprintf("good TODO, %s", currentUsername), nil

}
