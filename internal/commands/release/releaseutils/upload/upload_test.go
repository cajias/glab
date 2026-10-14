//go:build !integration

package upload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestReleaseUtilsUpload_AliasFilePathToAssetDirectPath(t *testing.T) {
	tests := []struct {
		name                 string
		givenReleaseAsset    *ReleaseAsset
		expectedReleaseAsset *ReleaseAsset
		expectedAliased      bool
	}{
		{
			name:                 "no filepath, no direct_asset_path",
			givenReleaseAsset:    &ReleaseAsset{Name: new("any-name")},
			expectedReleaseAsset: &ReleaseAsset{Name: new("any-name")},
			expectedAliased:      false,
		},
		{
			name:                 "no filepath, but direct_asset_path",
			givenReleaseAsset:    &ReleaseAsset{Name: new("any-name"), DirectAssetPath: new("/any-path")},
			expectedReleaseAsset: &ReleaseAsset{Name: new("any-name"), DirectAssetPath: new("/any-path")},
			expectedAliased:      false,
		},
		{
			name:                 "filepath, but not direct_asset_path",
			givenReleaseAsset:    &ReleaseAsset{Name: new("any-name"), FilePath: new("/any-path")},
			expectedReleaseAsset: &ReleaseAsset{Name: new("any-name"), DirectAssetPath: new("/any-path")},
			expectedAliased:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aliased, err := aliasFilePathToDirectAssetPath(tt.givenReleaseAsset)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedAliased, aliased)
			assert.Equal(t, tt.expectedReleaseAsset, tt.givenReleaseAsset)
		})
	}
}

func TestReleaseUtilsUpload_AliasFilePathToAssetDirectPath_Conflict(t *testing.T) {
	asset := &ReleaseAsset{
		FilePath:        new("/any-path"),
		DirectAssetPath: new("/any-path"),
	}

	aliased, err := aliasFilePathToDirectAssetPath(asset)

	target := &ConflictDirectAssetPathError{}
	require.ErrorAs(t, err, &target)
	assert.False(t, aliased)
}

func TestReleaseUtilsUpload_UploadFiles_ProjectMarkdownFiles(t *testing.T) {
	t.Parallel()

	// GIVEN
	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	testReleaseFile := bytes.NewBufferString("Hello World")
	uploadCtx := &Context{
		Client: tc.Client,
		IO:     ios,
		AssetFiles: []*ReleaseFile{
			{
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(testReleaseFile), nil
				},
				Name: "test-release-file.txt",
				Path: "./foobar/test-release-file.txt",
				Type: new(gitlab.OtherLinkType),
			},
		},
		AssetsLinks: []*ReleaseAsset{},
	}

	// setup mock exceptions
	gomock.InOrder(
		tc.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("any-project", gomock.Any(), "test-release-file.txt", gomock.Any()).
			Return(&gitlab.MarkdownUploadedFile{FullPath: "test-release-file.txt"}, nil, nil),
		tc.MockReleaseLinks.EXPECT().CreateReleaseLink("any-project", "42.0.0", gomock.Any(), gomock.Any()),
	)

	// WHEN
	err := uploadCtx.UploadFiles(t.Context(), "any-project", "42.0.0", DefaultReleasePackageName, false)

	// THEN
	require.NoError(t, err)
}

func TestReleaseUtilsUpload_UploadFiles_GenericPackageRegistry(t *testing.T) {
	t.Parallel()

	// GIVEN
	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	testReleaseFile := bytes.NewBufferString("Hello World")
	uploadCtx := &Context{
		Client: tc.Client,
		IO:     ios,
		AssetFiles: []*ReleaseFile{
			{
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(testReleaseFile), nil
				},
				Name: "test-release-file.txt",
				Path: "./foobar/test-release-file.txt",
				Type: new(gitlab.OtherLinkType),
			},
		},
		AssetsLinks: []*ReleaseAsset{},
	}

	// setup mock exceptions
	gomock.InOrder(
		tc.MockGenericPackages.EXPECT().
			PublishPackageFile("any-project", DefaultReleasePackageName, "42.0.0", "test-release-file.txt", gomock.Any(), nil, gomock.Any()),
		tc.MockGenericPackages.EXPECT().
			FormatPackageURL("any-project", DefaultReleasePackageName, "42.0.0", "test-release-file.txt"),
		tc.MockReleaseLinks.EXPECT().CreateReleaseLink("any-project", "42.0.0", gomock.Any(), gomock.Any()),
	)

	// WHEN
	err := uploadCtx.UploadFiles(t.Context(), "any-project", "42.0.0", DefaultReleasePackageName, true)

	// THEN
	require.NoError(t, err)
}

func newTestReleaseFiles(count int) []*ReleaseFile {
	files := make([]*ReleaseFile, count)
	for i := range files {
		name := fmt.Sprintf("file-%d.txt", i)
		files[i] = &ReleaseFile{
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewBufferString(name)), nil
			},
			Name:  name,
			Label: name,
			Path:  "./" + name,
		}
	}
	return files
}

func TestReleaseUtilsUpload_UploadFiles_LinksFollowInputOrder(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	files := newTestReleaseFiles(uploadConcurrency)
	uploadCtx := &Context{Client: tc.Client, IO: ios, AssetFiles: files}

	done := make([]chan struct{}, len(files))
	for i := range done {
		done[i] = make(chan struct{})
	}
	for i, file := range files {
		tc.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("any-project", gomock.Any(), file.Name, gomock.Any()).
			DoAndReturn(func(_ any, _ io.Reader, name string, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectMarkdownUploadedFile, *gitlab.Response, error) {
				if i+1 < len(done) {
					select {
					case <-done[i+1]:
					case <-time.After(5 * time.Second):
						return nil, nil, errors.New("uploads did not run concurrently")
					}
				}
				close(done[i])
				return &gitlab.ProjectMarkdownUploadedFile{FullPath: "/uploads/" + name}, nil, nil
			})
	}
	var linkCalls []any
	for _, file := range files {
		linkCalls = append(linkCalls, tc.MockReleaseLinks.EXPECT().
			CreateReleaseLink("any-project", "42.0.0", gomock.Cond(func(opt *gitlab.CreateReleaseLinkOptions) bool {
				return *opt.Name == file.Label
			}), gomock.Any()))
	}
	gomock.InOrder(linkCalls...)

	err := uploadCtx.UploadFiles(t.Context(), "any-project", "42.0.0", DefaultReleasePackageName, false)

	require.NoError(t, err)
	assert.Nil(t, uploadCtx.AssetFiles)
}

func TestReleaseUtilsUpload_UploadFiles_LimitsConcurrency(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	files := newTestReleaseFiles(uploadConcurrency + 2)
	uploadCtx := &Context{Client: tc.Client, IO: ios, AssetFiles: files}

	var active, maxActive atomic.Int32
	var started sync.WaitGroup
	started.Add(uploadConcurrency)
	allSlotsBusy := make(chan struct{})
	go func() {
		started.Wait()
		close(allSlotsBusy)
	}()
	var startedCount atomic.Int32

	tc.MockGenericPackages.EXPECT().
		PublishPackageFile("any-project", DefaultReleasePackageName, "42.0.0", gomock.Any(), gomock.Any(), nil, gomock.Any()).
		DoAndReturn(func(_ any, _, _, _ string, _ io.Reader, _ *gitlab.PublishPackageFileOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GenericPackagesFile, *gitlab.Response, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				peak := maxActive.Load()
				if current <= peak || maxActive.CompareAndSwap(peak, current) {
					break
				}
			}
			if startedCount.Add(1) <= uploadConcurrency {
				started.Done()
			}
			select {
			case <-allSlotsBusy:
			case <-time.After(5 * time.Second):
				return nil, nil, errors.New("uploads did not run concurrently")
			}
			return &gitlab.GenericPackagesFile{}, nil, nil
		}).
		Times(len(files))
	tc.MockGenericPackages.EXPECT().
		FormatPackageURL("any-project", DefaultReleasePackageName, "42.0.0", gomock.Any()).
		Return("/packages/file", nil).
		Times(len(files))
	tc.MockReleaseLinks.EXPECT().
		CreateReleaseLink("any-project", "42.0.0", gomock.Any(), gomock.Any()).
		Times(len(files))

	err := uploadCtx.UploadFiles(t.Context(), "any-project", "42.0.0", DefaultReleasePackageName, true)

	require.NoError(t, err)
	assert.Equal(t, int32(uploadConcurrency), maxActive.Load())
}

func TestReleaseUtilsUpload_UploadFiles_UploadErrorCreatesNoLinks(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	files := newTestReleaseFiles(2)
	uploadCtx := &Context{Client: tc.Client, IO: ios, AssetFiles: files}
	uploadErr := errors.New("upload failed")

	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("any-project", gomock.Any(), files[0].Name, gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{FullPath: "/uploads/" + files[0].Name}, nil, nil).
		MaxTimes(1)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("any-project", gomock.Any(), files[1].Name, gomock.Any()).
		Return(nil, nil, uploadErr)
	tc.MockReleaseLinks.EXPECT().CreateReleaseLink(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	err := uploadCtx.UploadFiles(t.Context(), "any-project", "42.0.0", DefaultReleasePackageName, false)

	require.ErrorIs(t, err, uploadErr)
	assert.ErrorContains(t, err, files[1].Path)
}

func TestReleaseUtilsUpload_UploadFiles_CancelledContextUploadsNothing(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	uploadCtx := &Context{Client: tc.Client, IO: ios, AssetFiles: newTestReleaseFiles(3)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tc.MockProjectMarkdownUploads.EXPECT().UploadProjectMarkdown(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	tc.MockReleaseLinks.EXPECT().CreateReleaseLink(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	err := uploadCtx.UploadFiles(ctx, "any-project", "42.0.0", DefaultReleasePackageName, false)

	require.ErrorIs(t, err, context.Canceled)
}

func TestReleaseUtilsUpload_UploadFiles_PrintsOneLinePerFile(t *testing.T) {
	t.Parallel()

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	files := newTestReleaseFiles(3)
	uploadCtx := &Context{Client: tc.Client, IO: ios, AssetFiles: files}

	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("any-project", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{FullPath: "/uploads/file"}, nil, nil).
		Times(len(files))
	tc.MockReleaseLinks.EXPECT().CreateReleaseLink("any-project", "42.0.0", gomock.Any(), gomock.Any()).Times(len(files))

	err := uploadCtx.UploadFiles(t.Context(), "any-project", "42.0.0", DefaultReleasePackageName, false)

	require.NoError(t, err)
	assert.Equal(t, "• Uploading to release\tfile=./file-0.txt name=file-0.txt\n"+
		"• Uploading to release\tfile=./file-1.txt name=file-1.txt\n"+
		"• Uploading to release\tfile=./file-2.txt name=file-2.txt\n", stdout.String())
}
