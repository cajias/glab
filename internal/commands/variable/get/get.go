package get

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/variable/variableutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	scope        string
	key          string
	group        string
	inherited    bool
	outputFormat string
}

func NewCmdGet(f cmdutils.Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a variable for a project or group.",
		Long: heredoc.Docf(`
			Prints the value of a single variable by key. Use %[1]s--scope%[1]s to
			select a variable in a specific environment, or %[1]s--group%[1]s to read a
			group variable instead.

			By default the command reads one level only. Pass %[1]s--inherited%[1]s to
			fall back to the groups above the project and then to the instance,
			in the order CI resolves a variable: the project first, then each
			parent group closest first, then instance variables.

			A level that does not define the key, or that you cannot read, is
			skipped. Instance variables apply to every environment, so
			%[1]s--scope%[1]s does not filter them.
		`, "`"),
		Args: cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			glab variable get VAR_KEY
			glab variable get -g GROUP VAR_KEY
			glab variable get -s SCOPE VAR_KEY

			# Read the project, then its parent groups, then the instance
			glab variable get --inherited VAR_KEY`),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			if err := opts.validate(); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}
			return opts.run()
		},
	}

	cmd.Flags().StringVarP(&opts.scope, "scope", "s", "*", "The environment_scope of the variable. Values: all (*), or specific environments.")
	cmd.Flags().StringVarP(&opts.group, "group", "g", "", "Get variable for a group.")
	cmd.Flags().BoolVar(&opts.inherited, "inherited", false, "If the key is not defined at the selected level, look in the groups above it, then in instance variables.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	return cmd
}

func (o *options) complete(args []string) {
	o.key = args[0]
}

func (o *options) validate() error {
	if !variableutils.IsValidKey(o.key) {
		return cmdutils.FlagError{Err: fmt.Errorf("invalid key provided.\n%s", variableutils.ValidKeyMsg)}
	}

	return nil
}

func (o *options) run() error {
	// NOTE: this command can not only be used for projects,
	// so we have to manually check for the base repo, if it doesn't exist,
	// we bootstrap the client with the default hostname.
	var repoHost string
	if baseRepo, err := o.baseRepo(); err == nil {
		repoHost = baseRepo.RepoHost()
	}
	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	found, err := o.resolve(client)
	if err != nil {
		return err
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(found.payload)
	}

	// The value goes to stdout on its own, so a shell can capture it. Any
	// note about where it came from belongs on stderr.
	if found.missingFrom != "" {
		o.io.LogErrorf("%s is not defined in %s. Using the value from %s.\n", o.key, found.missingFrom, found.source)
	}
	o.io.LogInfof("%s", found.value)
	return nil
}

// variableMatch is a variable found at one level of the lookup.
type variableMatch struct {
	payload     any // printed as-is by --output json
	value       string
	source      string // the level that defined the key, for example "group acme"
	missingFrom string // the selected level when it did not define the key, empty otherwise
}

func (o *options) resolve(client *gitlab.Client) (*variableMatch, error) {
	if o.group != "" {
		return o.resolveFromGroup(client)
	}
	return o.resolveFromProject(client)
}

func (o *options) resolveFromProject(client *gitlab.Client) (*variableMatch, error) {
	baseRepo, err := o.baseRepo()
	if err != nil {
		return nil, err
	}

	project := baseRepo.FullName()
	variable, _, err := client.ProjectVariables.GetVariable(project, o.key, &gitlab.GetProjectVariableOptions{
		Filter: &gitlab.VariableFilter{EnvironmentScope: o.scope},
	})
	if err == nil {
		return &variableMatch{payload: variable, value: variable.Value, source: "project " + project}, nil
	}
	if !o.inherited || !skippableLevel(err) {
		return nil, err
	}

	// The project does not define the key. Walk the groups that hold it, then
	// instance variables.
	match, walkErr := o.walkUp(client, variableutils.GroupChain(variableutils.ParentNamespace(project)))
	if walkErr != nil {
		return nil, walkErr
	}
	if match == nil {
		// Nothing above the project defines it either, so report the original
		// project error, exactly as a run without --inherited would.
		return nil, err
	}
	match.missingFrom = "project " + project
	return match, nil
}

func (o *options) resolveFromGroup(client *gitlab.Client) (*variableMatch, error) {
	variable, _, err := client.GroupVariables.GetVariable(o.group, o.key, &gitlab.GetGroupVariableOptions{
		Filter: &gitlab.VariableFilter{EnvironmentScope: o.scope},
	})
	if err == nil {
		return &variableMatch{payload: variable, value: variable.Value, source: "group " + o.group}, nil
	}
	if !o.inherited || !skippableLevel(err) {
		return nil, err
	}

	group, pathErr := o.groupFullPath(client)
	if pathErr != nil {
		return nil, pathErr
	}

	match, walkErr := o.walkUp(client, variableutils.GroupChain(variableutils.ParentNamespace(group)))
	if walkErr != nil {
		return nil, walkErr
	}
	if match == nil {
		return nil, err
	}
	match.missingFrom = "group " + group
	return match, nil
}

// groupFullPath returns the path of the --group value. The parent groups come
// from that path, and a numeric ID does not carry it, so the API supplies it.
func (o *options) groupFullPath(client *gitlab.Client) (string, error) {
	if _, parseErr := strconv.ParseUint(o.group, 10, 64); parseErr == nil {
		group, _, err := client.Groups.GetGroup(o.group, &gitlab.GetGroupOptions{})
		if err != nil {
			return "", fmt.Errorf("finding the parent groups of group %s: %w", o.group, err)
		}
		return group.FullPath, nil
	}
	return o.group, nil
}

// walkUp reads each group in turn, then instance variables, and returns the
// first definition of the key. A nil match means no level defined it.
func (o *options) walkUp(client *gitlab.Client, groups []string) (*variableMatch, error) {
	for _, group := range groups {
		variable, _, err := client.GroupVariables.GetVariable(group, o.key, &gitlab.GetGroupVariableOptions{
			Filter: &gitlab.VariableFilter{EnvironmentScope: o.scope},
		})
		if err == nil {
			return &variableMatch{
				payload: variable,
				value:   variable.Value,
				source:  "group " + group,
			}, nil
		}
		if !skippableLevel(err) {
			return nil, err
		}
	}

	// Instance variables are not scoped to an environment, so --scope does not
	// apply here. Reading them needs administrator access, and a user without
	// it gets 403, which ends the walk without an error.
	variable, _, err := client.InstanceVariables.GetVariable(o.key)
	if err == nil {
		return &variableMatch{
			payload: variable,
			value:   variable.Value,
			source:  "instance variables",
		}, nil
	}
	if !skippableLevel(err) {
		return nil, err
	}

	return nil, nil
}

// skippableLevel reports whether a level can be passed over during a walk. A
// level that does not define the key answers 404, and a level the user cannot
// read answers 401 or 403. Any other failure is real and must surface.
func skippableLevel(err error) bool {
	var response *gitlab.ErrorResponse
	if !errors.As(err, &response) {
		return false
	}

	// Sentinel errors such as gitlab.ErrNotFound carry the status in the
	// struct and no HTTP response, so both places are read.
	status := response.StatusCode
	if status == 0 && response.Response != nil {
		status = response.Response.StatusCode
	}

	switch status {
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return true
	default:
		return false
	}
}
