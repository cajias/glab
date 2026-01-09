package cmdtest

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -typed -destination=./executor_mocks.go -package=cmdtest gitlab.com/gitlab-org/cli/internal/cmdutils Executor
