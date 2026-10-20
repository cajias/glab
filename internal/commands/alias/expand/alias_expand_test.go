//go:build !integration

package expand

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestExpandAlias(t *testing.T) {
	findShFunc := func() (string, error) {
		return "/usr/bin/sh", nil
	}

	cfg := config.NewFromString(heredoc.Doc(`
		aliases:
		  co: mr checkout
		  il: issue list --author="$1" --label="$2"
		  ia: issue list --author="$1" --assignee="$1"
		  ic: issue create --title "$1"
		  is: issue list --search=$1
		  iv: issue view $1 --comments
		  ten: issue list --label="$1" --search="$10"
		  il2: issue list --label="$2" --author="$1"
		  ip: issue list --search="price$"
		  i0: issue list --search="$0"
	`))

	type args struct {
		config config.Config
		argv   []string
	}
	tests := []struct {
		name         string
		args         args
		wantExpanded []string
		wantIsShell  bool
		wantErr      error
	}{
		{
			name: "no arguments",
			args: args{
				config: cfg,
				argv:   []string{},
			},
			wantExpanded: []string(nil),
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "too few arguments",
			args: args{
				config: cfg,
				argv:   []string{"glab"},
			},
			wantExpanded: []string(nil),
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "no expansion",
			args: args{
				config: cfg,
				argv:   []string{"glab", "mr", "status"},
			},
			wantExpanded: []string{"mr", "status"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "simple expansion",
			args: args{
				config: cfg,
				argv:   []string{"glab", "co"},
			},
			wantExpanded: []string{"mr", "checkout"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "adding arguments after expansion",
			args: args{
				config: cfg,
				argv:   []string{"glab", "co", "123"},
			},
			wantExpanded: []string{"mr", "checkout", "123"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "not enough arguments for expansion",
			args: args{
				config: cfg,
				argv:   []string{"glab", "il"},
			},
			wantExpanded: []string{},
			wantIsShell:  false,
			wantErr:      errors.New(`not enough arguments for alias: issue list --author="$1" --label="$2"`),
		},
		{
			name: "not enough arguments for expansion 2",
			args: args{
				config: cfg,
				argv:   []string{"glab", "il", "vilmibm"},
			},
			wantExpanded: []string{},
			wantIsShell:  false,
			wantErr:      errors.New(`not enough arguments for alias: issue list --author="vilmibm" --label="$2"`),
		},
		{
			name: "satisfy expansion arguments",
			args: args{
				config: cfg,
				argv:   []string{"glab", "il", "vilmibm", "help wanted"},
			},
			wantExpanded: []string{"issue", "list", "--author=vilmibm", "--label=help wanted"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "mixed positional and non-positional arguments",
			args: args{
				config: cfg,
				argv:   []string{"glab", "il", "vilmibm", "epic", "-R", "monalisa/testing"},
			},
			wantExpanded: []string{"issue", "list", "--author=vilmibm", "--label=epic", "-R", "monalisa/testing"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "dollar in expansion",
			args: args{
				config: cfg,
				argv:   []string{"glab", "ia", "$coolmoney$"},
			},
			wantExpanded: []string{"issue", "list", "--author=$coolmoney$", "--assignee=$coolmoney$"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "dollar in argument keeps the extra arguments",
			args: args{
				config: cfg,
				argv:   []string{"glab", "ia", "$coolmoney$", "-R", "monalisa/testing"},
			},
			wantExpanded: []string{"issue", "list", "--author=$coolmoney$", "--assignee=$coolmoney$", "-R", "monalisa/testing"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "placeholder text in argument is not a placeholder",
			args: args{
				config: cfg,
				argv:   []string{"glab", "ic", "Costs $5 now", "--description", "x"},
			},
			wantExpanded: []string{"issue", "create", "--title", "Costs $5 now", "--description", "x"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "placeholder text in argument is not filled by another argument",
			args: args{
				config: cfg,
				argv:   []string{"glab", "il", "$2", "bug"},
			},
			wantExpanded: []string{"issue", "list", "--author=$2", "--label=bug"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "quotes in argument are kept",
			args: args{
				config: cfg,
				argv:   []string{"glab", "ic", `Fix "login" page`},
			},
			wantExpanded: []string{"issue", "create", "--title", `Fix "login" page`},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "argument with spaces in an unquoted placeholder stays one argument",
			args: args{
				config: cfg,
				argv:   []string{"glab", "is", "login page", "-R", "monalisa/testing"},
			},
			wantExpanded: []string{"issue", "list", "--search=login page", "-R", "monalisa/testing"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "argument starting with a hash in an unquoted placeholder is not a comment",
			args: args{
				config: cfg,
				argv:   []string{"glab", "iv", "#123"},
			},
			wantExpanded: []string{"issue", "view", "#123", "--comments"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "placeholders out of order keep the extra arguments",
			args: args{
				config: cfg,
				argv:   []string{"glab", "il2", "vilmibm", "epic", "-R", "monalisa/testing"},
			},
			wantExpanded: []string{"issue", "list", "--label=epic", "--author=vilmibm", "-R", "monalisa/testing"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "dollar without placeholder keeps the extra arguments",
			args: args{
				config: cfg,
				argv:   []string{"glab", "ip", "-R", "monalisa/testing"},
			},
			wantExpanded: []string{"issue", "list", "--search=price$", "-R", "monalisa/testing"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "dollar zero is not a placeholder",
			args: args{
				config: cfg,
				argv:   []string{"glab", "i0"},
			},
			wantExpanded: []string{"issue", "list", "--search=$0"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "two-digit placeholder",
			args: args{
				config: cfg,
				argv:   []string{"glab", "ten", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"},
			},
			wantExpanded: []string{"issue", "list", "--label=one", "--search=ten"},
			wantIsShell:  false,
			wantErr:      nil,
		},
		{
			name: "not enough arguments for a two-digit placeholder",
			args: args{
				config: cfg,
				argv:   []string{"glab", "ten", "one"},
			},
			wantExpanded: []string{},
			wantIsShell:  false,
			wantErr:      errors.New(`not enough arguments for alias: issue list --label="one" --search="$10"`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotExpanded, gotIsShell, err := ExpandAlias(tt.args.config, tt.args.argv, findShFunc)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.wantErr.Error() != err.Error() {
					t.Fatalf("expected error %q, got %q", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("got error: %v", err)
			}
			if !reflect.DeepEqual(gotExpanded, tt.wantExpanded) {
				t.Errorf("ExpandAlias() gotExpanded = %v, want %v", gotExpanded, tt.wantExpanded)
			}
			if gotIsShell != tt.wantIsShell {
				t.Errorf("ExpandAlias() gotIsShell = %v, want %v", gotIsShell, tt.wantIsShell)
			}
		})
	}
}
