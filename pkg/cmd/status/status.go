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
	IO              *iostreams.IOStreams
	HasRepoOverride bool
	Org             string
	Exclude         string
}

func NewCmdStatus(f *cmdutil.Factory, runF func(*StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{}
	opts.HttpClient = f.HttpClient
	opts.IO = f.IOStreams
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print information about relevant issues, pull requests, and notifications across repositories",
		Long:  "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO decide if I want to bother implementing this
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

	cmd.Flags().StringVarP(&opts.Org, "org", "o", "", "Report status within an organization")
	cmd.Flags().StringVarP(&opts.Exclude, "exclude", "e", "", "Comma separated list of repos to exclude in owner/name format")

	// TODO ability to run for an org
	// TODO ability to exclude repositories
	// TODO? ability to filter to single repository
	// TODO break out sections into individual subcommands (but prob save for future PR)

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
		Owner struct {
			Login string
		}
		FullName string `json:"full_name"`
	}
}

type StatusItem struct {
	Repository string // owner/repo
	Identifier string // eg cli/cli#1234 or just 1234
	preview    string // eg This is the truncated body of something...
	Reason     string // only used in repo activity
}

func (s StatusItem) Preview() string {
	return strings.ReplaceAll(strings.ReplaceAll(s.preview, "\r", ""), "\n", " ")
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
	Type string
	Org  struct {
		Login string
	}
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

type SearchResult struct {
	Type       string `json:"__typename"`
	UpdatedAt  time.Time
	Title      string
	Number     int
	Repository struct {
		NameWithOwner string
	}
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

type StatusGetter struct {
	Client         *http.Client
	Org            string
	Exclude        string
	AssignedPRs    []StatusItem
	AssignedIssues []StatusItem
	Mentions       []StatusItem
	ReviewRequests []StatusItem
	RepoActivity   []StatusItem
}

func NewStatusGetter(client *http.Client, org, exclude string) *StatusGetter {
	return &StatusGetter{
		Client:  client,
		Org:     org,
		Exclude: exclude,
	}
}

// These are split up by endpoint since it is along that boundary we parallelize
// work

// Populate .Mentions
func (s *StatusGetter) LoadNotifications() error {
	apiClient := api.NewClientFromHTTP(s.Client)
	query := url.Values{}
	query.Add("per_page", "100")
	query.Add("participating", "true")
	query.Add("all", "true")

	// this sucks, having to fetch so much :/ but it was the only way in my
	// testing to really get enough mentions. I would love to be able to just
	// filter for mentions but it does not seem like the notifications API can
	// do that. I'd switch to the GraphQL version, but to my knowledge that does
	// not work with PATs right now.
	var ns []Notification
	var resp []Notification
	pages := 3
	for page := 1; page <= pages; page++ {
		query.Add("page", fmt.Sprintf("%d", page))
		p := fmt.Sprintf("notifications?%s", query.Encode())
		// behavior when only one page?
		err := apiClient.REST(ghinstance.Default(), "GET", p, nil, &resp)
		if err != nil {
			return fmt.Errorf("could not get notifications: %w", err)
		}
		ns = append(ns, resp...)
	}

	s.Mentions = []StatusItem{}

	for _, n := range ns {
		if n.Reason != "mention" {
			continue
		}

		if s.Org != "" && n.Repository.Owner.Login != s.Org {
			continue
		}

		if actual, err := actualMention(s.Client, n); actual != "" && err == nil {
			// I'm so sorry
			split := strings.Split(n.Subject.URL, "/")
			s.Mentions = append(s.Mentions, StatusItem{
				Repository: n.Repository.FullName,
				Identifier: fmt.Sprintf("%s#%s", n.Repository.FullName, split[len(split)-1]),
				preview:    actual,
			})
		} else if err != nil {
			return fmt.Errorf("could not fetch comment: %w", err)
		}
	}

	return nil
}

// Populate .AssignedPRs, .AssignedIssues, .ReviewRequests
func (s *StatusGetter) LoadSearchResults() error {
	q := `
	query AssignedSearch {
	  assignments: search(first: 25, type: ISSUE, query:"assignee:@me state:open%s") {
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

	orgFilter := ""
	if s.Org != "" {
		orgFilter = " org:" + s.Org
	}
	q = fmt.Sprintf(q, orgFilter)

	apiClient := api.NewClientFromHTTP(s.Client)

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
		return fmt.Errorf("could not search for assignments: %w", err)
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

	s.AssignedIssues = []StatusItem{}
	s.AssignedPRs = []StatusItem{}
	s.ReviewRequests = []StatusItem{}

	for _, i := range issues {
		s.AssignedIssues = append(s.AssignedIssues, StatusItem{
			Repository: i.Repository.NameWithOwner,
			Identifier: fmt.Sprintf("%s#%d", i.Repository.NameWithOwner, i.Number),
			preview:    i.Title,
		})
	}

	for _, pr := range prs {
		s.AssignedPRs = append(s.AssignedPRs, StatusItem{
			Repository: pr.Repository.NameWithOwner,
			Identifier: fmt.Sprintf("%s#%d", pr.Repository.NameWithOwner, pr.Number),
			preview:    pr.Title,
		})
	}

	for _, r := range reviewRequested {
		s.ReviewRequests = append(s.ReviewRequests, StatusItem{
			Repository: r.Repository.NameWithOwner,
			Identifier: fmt.Sprintf("%s#%d", r.Repository.NameWithOwner, r.Number),
			preview:    r.Title,
		})
	}

	return nil
}

// Populate .RepoActivity
func (s *StatusGetter) LoadEvents() error {
	apiClient := api.NewClientFromHTTP(s.Client)
	query := url.Values{}
	query.Add("per_page", "100")

	// TODO caching
	currentUsername, err := api.CurrentLoginName(apiClient, ghinstance.Default())
	if err != nil {
		return err
	}

	var events []Event
	var resp []Event
	pages := 3
	for page := 1; page <= pages; page++ {
		query.Add("page", fmt.Sprintf("%d", page))
		p := fmt.Sprintf("users/%s/received_events?%s", currentUsername, query.Encode())
		// TODO handle fewer pages (ie page up not down)
		err := apiClient.REST(ghinstance.Default(), "GET", p, nil, &resp)
		if err != nil {
			return fmt.Errorf("could not get events: %w", err)
		}
		events = append(events, resp...)
	}

	s.RepoActivity = []StatusItem{}

	for _, e := range events {
		if s.Org != "" && e.Org.Login != s.Org {
			continue
		}
		si := StatusItem{}
		var number int
		switch e.Type {
		case "IssuesEvent":
			if e.Payload.Action != "opened" {
				continue
			}
			si.Reason = "new issue"
			si.preview = e.Payload.Issue.Title
			number = e.Payload.Issue.Number
		case "PullRequestEvent":
			if e.Payload.Action != "opened" {
				continue
			}
			si.Reason = "new PR"
			si.preview = e.Payload.PullRequest.Title
			number = e.Payload.PullRequest.Number
		case "IssueCommentEvent":
			si.Reason = "comment on " + e.Payload.Issue.Title
			si.preview = e.Payload.Comment.Body
			number = e.Payload.Issue.Number
		default:
			continue
		}
		si.Repository = e.Repo.Name
		si.Identifier = fmt.Sprintf("%s#%d", e.Repo.Name, number)
		s.RepoActivity = append(s.RepoActivity, si)
	}

	return nil
}

func statusRun(opts *StatusOptions) error {
	client, err := opts.HttpClient()
	if err != nil {
		return fmt.Errorf("could not create client: %w", err)
	}

	sg := NewStatusGetter(client, opts.Org, opts.Exclude)

	err = sg.LoadNotifications()
	if err != nil {
		return fmt.Errorf("could not load notifications: %w", err)
	}
	mentions := sg.Mentions

	err = sg.LoadEvents()
	if err != nil {
		return fmt.Errorf("could not load events: %w", err)
	}

	err = sg.LoadSearchResults()
	if err != nil {
		return fmt.Errorf("failed to search: %w", err)
	}

	cs := opts.IO.ColorScheme()
	out := opts.IO.Out
	fullWidth := opts.IO.TerminalWidth()
	halfWidth := (fullWidth / 2) - 2

	idStyle := cs.Cyan
	leftHalfStyle := lipgloss.NewStyle().Width(halfWidth).Padding(0).BorderRight(true).BorderStyle(lipgloss.NormalBorder())
	rightHalfStyle := lipgloss.NewStyle().Width(halfWidth).Padding(0)

	section := func(header string, items []StatusItem, width, rowLimit int) (string, error) {
		tableOut := &bytes.Buffer{}
		fmt.Fprintln(tableOut, cs.Bold(header))
		tp := utils.NewTablePrinterWithOptions(opts.IO, utils.TablePrinterOptions{
			IsTTY:    opts.IO.IsStdoutTTY(),
			MaxWidth: width,
			Out:      tableOut,
		})
		if len(items) == 0 {
			tp.AddField("Nothing here ^_^", nil, nil)
			tp.EndRow()
		} else {
			for i, si := range items {
				if i == rowLimit {
					break
				}
				tp.AddField(si.Identifier, nil, idStyle)
				if si.Reason != "" {
					tp.AddField(si.Reason, nil, nil)
				}
				tp.AddField(si.Preview(), nil, nil)
				tp.EndRow()
			}
		}

		err := tp.Render()
		if err != nil {
			return "", err
		}

		return tableOut.String(), nil
	}

	mSection, err := section("Mentions", mentions, halfWidth, 5)
	if err != nil {
		return fmt.Errorf("failed to render 'Mentions': %w", err)
	}
	mSection = rightHalfStyle.Render(mSection)

	rrSection, err := section("Review Requests", sg.ReviewRequests, halfWidth, 5)
	if err != nil {
		return fmt.Errorf("failed to render 'Review Requests': %w", err)
	}
	rrSection = leftHalfStyle.Render(rrSection)

	prSection, err := section("Assigned PRs", sg.AssignedPRs, halfWidth, 5)
	if err != nil {
		return fmt.Errorf("failed to render 'Assigned PRs': %w", err)
	}
	prSection = rightHalfStyle.Render(prSection)

	issueSection, err := section("Assigned Issues", sg.AssignedIssues, halfWidth, 5)
	if err != nil {
		return fmt.Errorf("failed to render 'Assigned Issues': %w", err)
	}
	issueSection = leftHalfStyle.Render(issueSection)

	raSection, err := section("Repository Activity", sg.RepoActivity, fullWidth, 10)
	if err != nil {
		return fmt.Errorf("failed to render 'Repository Activity': %w", err)
	}

	fmt.Fprintln(out, lipgloss.JoinHorizontal(lipgloss.Top, issueSection, prSection))
	fmt.Fprintln(out, lipgloss.JoinHorizontal(lipgloss.Top, rrSection, mSection))
	fmt.Fprintln(out, raSection)

	// TODO
	// - goroutines for each network call + subsequent processing
	// - ensure caching appropriately

	return nil
}
