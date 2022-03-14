package status

import (
	"testing"

	"github.com/cli/cli/v2/pkg/httpmock"
)

func TestNewCmdStatus(t *testing.T) {
	tests := []struct {
		name  string
		cli   string
		wants StatusOptions
	}{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TODO
		})
	}
}

func TestStatusRun(t *testing.T) {
	tests := []struct {
		name      string
		httpStubs func(*httpmock.Registry)
		opts      *StatusOptions
		// TODO this is going to suck
		wantOut string
	}{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TODO
		})
	}
}
