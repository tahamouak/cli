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

/*
• "repo activity"
	• using notification API
	• new issues
	• new prs
	• comments
• mentions
	• using notifications API
• review requests
	• using search API
• pr assignments
	• using search API
• issue assignments
	• using search API
*/

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

func statusRun(opts *StatusOptions) error {
	client, err := opts.HttpClient()
	if err != nil {
		return fmt.Errorf("could not create client: %w", err)
	}
	ns, err := getNotifications(client)
	if err != nil {
		return err
	}

	mentions := []Notification{}
	newIssues := []Notification{}
	newPRs := []Notification{}
	comments := []Notification{}
	reviewRequests := []Notification{}

	for _, n := range ns {
		if n.Subject.Type == "Discussion" || n.Subject.Type == "Release" {
			continue
		}

		if n.Reason == "mention" {
			// TODO error handling
			if actual, err := actualMention(client, n); actual && err != nil {
				mentions = append(mentions, n)
			} else {
				comments = append(comments, n)
			}
			continue
		}

		if n.Subject.LatestCommentURL == "" && n.Reason == "subscribed" {
			if n.Subject.Type == "PullRequest" {
				newPRs = append(newPRs, n)
			} else if n.Subject.Type == "Issue" {
				newIssues = append(newIssues, n)
			} else {
				// TODO i donno
				fmt.Printf("DBG %#v\n", n)
			}
			continue
		}

		if n.Reason == "review_requested" {
			reviewRequests = append(reviewRequests, n)
			continue
		}

		comments = append(comments, n)
	}

	// this is picking up stuff like team mentions. i should handle those explicitly.

	fmt.Println("MENTIONS")
	for _, n := range mentions {
		fmt.Printf("%s %s %s %s %s\n", n.Reason, n.Subject.Title, n.Subject.Type, n.Subject.URL, n.Subject.LatestCommentURL)
	}

	fmt.Println("NEW PRs")
	for _, n := range newPRs {
		fmt.Printf("%s %s %s %s %s\n", n.Reason, n.Subject.Title, n.Subject.Type, n.Subject.URL, n.Subject.LatestCommentURL)
	}

	fmt.Println("NEW ISSUES")
	for _, n := range newIssues {
		fmt.Printf("%s %s %s %s %s\n", n.Reason, n.Subject.Title, n.Subject.Type, n.Subject.URL, n.Subject.LatestCommentURL)
	}

	fmt.Println("COMMENTS")
	for _, n := range comments {
		fmt.Printf("%s %s %s %s %s\n", n.Reason, n.Subject.Title, n.Subject.Type, n.Subject.URL, n.Subject.LatestCommentURL)
	}

	fmt.Println("REVIEW REQUESTS")
	for _, n := range reviewRequests {
		fmt.Printf("%s %s %s %s %s\n", n.Reason, n.Subject.Title, n.Subject.Type, n.Subject.URL, n.Subject.LatestCommentURL)
	}

	// should i attempt to shoehorn all of this into a single giant graphql
	// query? i guess everything that is in graphql should be trated that way.

	// TODO review requests -- GQL search
	// TODO pr assignments -- GQL search
	// TODO issue assignments -- GQL search
	// TODO discussions -- GQL search
	// TODO mentions -- GQL, apparently. can this include discussions? continue to study mislav's extension

	// TODO figure out if this could work:
	// TODO repo activity -- REST
	// I think that /users/vilmibm/events might be good enough, but need to
	// analyze the JSON back and think about it.

	// this is sadly infeasible since discussions are scoped to repo
	// an option is to figure out what repos are active then get discussions for
	// them, but it would be impossible to enumerate every repo a user has
	// access to and get discussion listings.
	// TODO discussions -- GQL query

	// so this looks like i can parallel 3 requests -- two RESTs and a big ugly GQL
	return nil
}
