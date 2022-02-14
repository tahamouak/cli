package status

import (
	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type StatusOptions struct {
	BaseRepo        func() (ghrepo.Interface, error)
	HasRepoOverride bool
	Org             string
}

func NewCmdStatus(f *cmdutil.Factory, runF func(*StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{}
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

type gqlResults struct {
	ReviewRequests []api.PullRequest
	AssignedPRs    []api.PullRequest
	AssignedIssues []api.PullRequest
}

func statusRun(opts *StatusOptions) error {
	// should i attempt to shoehorn all of this into a single giant graphql
	// query? i guess everything that is in graphql should be trated that way.

	// TODO review requests -- GQL search
	// TODO pr assignments -- GQL search
	// TODO issue assignments -- GQL search
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
