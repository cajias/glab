//go:build !integration

package list

import (
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/acarl005/stripansi"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ScheduleList(t *testing.T) {
	io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	getSchedules = func(client *gitlab.Client, l *gitlab.ListPipelineSchedulesOptions, repo string) ([]*gitlab.PipelineSchedule, *gitlab.Response, error) {
		_, err := f.BaseRepo()
		if err != nil {
			return nil, nil, err
		}

		return []*gitlab.PipelineSchedule{
			{
				ID:          1,
				Description: "foo",
				Cron:        "* * * * *",
				Owner: &gitlab.User{
					ID:       1,
					Username: "bar",
				},
				Active: true,
			},
		}, nil, nil
	}

	cmd := NewCmdList(f)
	cmdutils.EnableRepoOverride(cmd, f)

	t.Run("Schedule exists", func(t *testing.T) {
		_, err := cmd.ExecuteC()
		if err != nil {
			t.Fatal(err)
		}

		out := stripansi.Strip(stdout.String())

		assert.Contains(t, out, "1\tfoo\t* * * * *\tbar\ttrue")
		assert.Empty(t, stderr.String())
	})
}

func Test_NoScheduleList(t *testing.T) {
	io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	stubFactory := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	getSchedules = func(*gitlab.Client, *gitlab.ListPipelineSchedulesOptions, string) ([]*gitlab.PipelineSchedule, *gitlab.Response, error) {
		_, err := stubFactory.BaseRepo()
		if err != nil {
			return nil, nil, err
		}

		return nil, nil, nil
	}

	cmd := NewCmdList(stubFactory)
	cmdutils.EnableRepoOverride(cmd, stubFactory)

	t.Run("No schedules exist", func(t *testing.T) {
		_, err := cmd.ExecuteC()
		if err != nil {
			t.Fatal(err)
		}

		out := stripansi.Strip(stdout.String())

		assert.Contains(t, out, "No schedules available on")
		assert.Empty(t, stderr.String())
	})
}

func TestScheduleList_HeaderTotal(t *testing.T) {
	schedules := []*gitlab.PipelineSchedule{
		{ID: 1, Description: "foo", Cron: "* * * * *", Owner: &gitlab.User{ID: 1, Username: "bar"}, Active: true},
	}

	tests := []struct {
		name       string
		cli        string
		resp       *gitlab.Response
		wantHeader string
	}{
		{
			name:       "shows the total from the API",
			cli:        "--per-page 5 --page 3",
			resp:       &gitlab.Response{CurrentPage: 3, TotalPages: 3, TotalItems: 14},
			wantHeader: "Showing 1 of 14 schedules on OWNER/REPO. (Page 3)\n",
		},
		{
			name:       "without a total from the API",
			cli:        "",
			resp:       &gitlab.Response{CurrentPage: 1, NextPage: 2},
			wantHeader: "Showing 1 schedule on OWNER/REPO. (Page 1)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
			f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
				hosts:
				  gitlab.com:
				    username: monalisa
				    token: OTOKEN
			`))))

			getSchedules = func(*gitlab.Client, *gitlab.ListPipelineSchedulesOptions, string) ([]*gitlab.PipelineSchedule, *gitlab.Response, error) {
				return schedules, tt.resp, nil
			}

			cmd := NewCmdList(f)
			cmdutils.EnableRepoOverride(cmd, f)

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			_, err = cmd.ExecuteC()
			require.NoError(t, err)

			out := stripansi.Strip(stdout.String())
			assert.True(t, strings.HasPrefix(out, tt.wantHeader), "got %q", out)
			assert.Empty(t, stderr.String())
		})
	}
}

func TestScheduleList_JSON(t *testing.T) {
	t.Parallel()

	io, _, stdout, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	getSchedules = func(client *gitlab.Client, l *gitlab.ListPipelineSchedulesOptions, repo string) ([]*gitlab.PipelineSchedule, *gitlab.Response, error) {
		return []*gitlab.PipelineSchedule{
			{
				ID:          1,
				Description: "foo",
				Cron:        "* * * * *",
				Owner: &gitlab.User{
					ID:       1,
					Username: "bar",
				},
				Active: true,
			},
		}, nil, nil
	}

	cmd := NewCmdList(f)
	cmdutils.EnableRepoOverride(cmd, f)

	argv, err := shlex.Split("-F json")
	require.NoError(t, err)
	cmd.SetArgs(argv)

	_, err = cmd.ExecuteC()
	require.NoError(t, err)

	out := stdout.String()
	assert.Contains(t, out, `"id":1`)
	assert.Contains(t, out, `"description":"foo"`)
	assert.Contains(t, out, `"cron":"* * * * *"`)
	assert.Contains(t, out, `"active":true`)
	assert.Empty(t, stderr.String())
}
