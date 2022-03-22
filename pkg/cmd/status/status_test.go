package status

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/httpmock"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
)

func TestNewCmdStatus(t *testing.T) {
	tests := []struct {
		name  string
		cli   string
		wants StatusOptions
	}{
		{
			name: "defaults",
		},
		{
			name: "org",
			cli:  "-o cli",
			wants: StatusOptions{
				Org: "cli",
			},
		},
		{
			name: "exclude",
			cli:  "-e cli/cli,cli/go-gh",
			wants: StatusOptions{
				Exclude: "cli/cli,cli/go-gh",
			},
		},
	}

	for _, tt := range tests {
		io, _, _, _ := iostreams.Test()
		// TODO do I care
		io.SetStdinTTY(true)
		io.SetStdoutTTY(true)

		f := &cmdutil.Factory{
			IOStreams: io,
		}
		t.Run(tt.name, func(t *testing.T) {
			argv, err := shlex.Split(tt.cli)
			assert.NoError(t, err)

			var gotOpts *StatusOptions
			cmd := NewCmdStatus(f, func(opts *StatusOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			_, err = cmd.ExecuteC()
			assert.NoError(t, err)

			assert.Equal(t, tt.wants.Org, gotOpts.Org)
			assert.Equal(t, tt.wants.Exclude, gotOpts.Exclude)
		})
	}
}

func TestStatusRun(t *testing.T) {
	stubMentions := func(reg *httpmock.Registry) {
		reg.Register(
			httpmock.REST("GET", "repos/rpd/todo/issues/110"),
			httpmock.StringResponse(`{"body":""}`))
		// TODO fill in rest, decide which are proper mentions
	}
	tests := []struct {
		name      string
		httpStubs func(*httpmock.Registry)
		opts      *StatusOptions
		// TODO this is going to suck
		wantOut    string
		wantErrMsg string
	}{
		{
			name: "nothing",
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL("UserCurrent"),
					httpmock.StringResponse(`{"data": {"viewer": {"login": "jillvalentine"}}}`))
				reg.Register(
					httpmock.GraphQL("AssignedSearch"),
					httpmock.StringResponse(`{"data": { "assignments": {"edges": [] }, "reviewRequested": {"edges": []}}}`))
				reg.Register(
					httpmock.REST("GET", "notifications"),
					httpmock.StringResponse(`[]`))
				reg.Register(
					httpmock.REST("GET", "users/jillvalentine/received_events"),
					httpmock.StringResponse(`[]`))
			},
			opts:    &StatusOptions{},
			wantOut: "Assigned Issues                       │Assigned PRs                          \nNothing here ^_^                      │Nothing here ^_^                      \n                                      │                                      \nReview Requests                       │Mentions                              \nNothing here ^_^                      │Nothing here ^_^                      \n                                      │                                      \nRepository Activity\nNothing here ^_^\n\n",
		},
		{
			name: "something",
			httpStubs: func(reg *httpmock.Registry) {
				stubMentions(reg)
				reg.Register(
					httpmock.GraphQL("UserCurrent"),
					httpmock.StringResponse(`{"data": {"viewer": {"login": "jillvalentine"}}}`))
				reg.Register(
					httpmock.GraphQL("AssignedSearch"),
					httpmock.FileResponse("./fixtures/search.json"))
				reg.Register(
					httpmock.REST("GET", "notifications"),
					httpmock.FileResponse("./fixtures/notifications.json"))
				reg.Register(
					httpmock.REST("GET", "users/jillvalentine/received_events"),
					httpmock.FileResponse("./fixtures/events.json"))
			},
			opts:    &StatusOptions{},
			wantOut: "TODO",
		},
		{
			name: "exclude a repository",
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL("UserCurrent"),
					httpmock.StringResponse(`{"data": {"viewer": {"login": "jillvalentine"}}}`))
				reg.Register(
					httpmock.GraphQL("AssignedSearch"),
					httpmock.FileResponse("./fixtures/search.json"))
				reg.Register(
					httpmock.REST("GET", "notifications"),
					httpmock.FileResponse("./fixtures/notifications.json"))
				reg.Register(
					httpmock.REST("GET", "users/jillvalentine/received_events"),
					httpmock.FileResponse("./fixtures/events.json"))
			},
			opts: &StatusOptions{
				Exclude: "wesker/evil,umbrella/bad",
			},
			wantOut: "TODO",
		},
		{
			name: "filter to an org",
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL("UserCurrent"),
					httpmock.StringResponse(`{"data": {"viewer": {"login": "jillvalentine"}}}`))
				reg.Register(
					httpmock.GraphQL("AssignedSearch"),
					httpmock.FileResponse("./fixtures/search.json"))
				reg.Register(
					httpmock.REST("GET", "notifications"),
					httpmock.FileResponse("./fixtures/notifications.json"))
				reg.Register(
					httpmock.REST("GET", "users/jillvalentine/received_events"),
					httpmock.FileResponse("./fixtures/events.json"))
			},
			opts: &StatusOptions{
				Org: "rpd",
			},
			wantOut: "TODO",
		},
	}

	for _, tt := range tests {
		reg := &httpmock.Registry{}
		tt.httpStubs(reg)
		tt.opts.HttpClient = func() (*http.Client, error) {
			return &http.Client{Transport: reg}, nil
		}
		tt.opts.CachedClient = func(c *http.Client, _ time.Duration) *http.Client {
			return c
		}
		io, _, stdout, _ := iostreams.Test()
		// TODO do i care
		io.SetStdoutTTY(true)
		tt.opts.IO = io

		t.Run(tt.name, func(t *testing.T) {
			err := statusRun(tt.opts)
			if tt.wantErrMsg != "" {
				assert.Equal(t, tt.wantErrMsg, err.Error())
				return
			}

			assert.NoError(t, err)

			assert.Equal(t, tt.wantOut, stdout.String())
			reg.Verify(t)
		})
	}
}
