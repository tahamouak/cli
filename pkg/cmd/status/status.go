package status

import (
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

func statusRun(opts *StatusOptions) error {
	return nil
}
