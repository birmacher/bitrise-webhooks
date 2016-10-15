package gitlab

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"github.com/bitrise-io/bitrise-webhooks/bitriseapi"
	"github.com/bitrise-io/go-utils/pointers"
	"github.com/stretchr/testify/require"
)

func Test_detectContentTypeAndEventID(t *testing.T) {
	t.Log("Code Push event")
	{
		header := http.Header{
			"X-Gitlab-Event": {"Push Hook"},
			"Content-Type":   {"application/json"},
		}
		contentType, eventID, err := detectContentTypeAndEventID(header)
		require.NoError(t, err)
		require.Equal(t, "application/json", contentType)
		require.Equal(t, "Push Hook", eventID)
	}

	t.Log("Merge Request event - should handle")
	{
		header := http.Header{
			"X-Gitlab-Event": {"Merge Request Hook"},
			"Content-Type":   {"application/json"},
		}
		contentType, glEvent, err := detectContentTypeAndEventID(header)
		require.NoError(t, err)
		require.Equal(t, "application/json", contentType)
		require.Equal(t, "Merge Request Hook", glEvent)
	}

	t.Log("Unsupported event - will be handled in Transform")
	{
		header := http.Header{
			"X-Gitlab-Event": {"Tag Push Hook"},
			"Content-Type":   {"application/json"},
		}
		contentType, eventID, err := detectContentTypeAndEventID(header)
		require.NoError(t, err)
		require.Equal(t, "application/json", contentType)
		require.Equal(t, "Tag Push Hook", eventID)
	}

	t.Log("Missing X-Gitlab-Event header")
	{
		header := http.Header{
			"Content-Type": {"application/json"},
		}
		contentType, eventID, err := detectContentTypeAndEventID(header)
		require.EqualError(t, err, "Issue with X-Gitlab-Event Header: No value found in HEADER for the key: X-Gitlab-Event")
		require.Equal(t, "", contentType)
		require.Equal(t, "", eventID)
	}

	t.Log("Missing Content-Type")
	{
		header := http.Header{
			"X-Gitlab-Event": {"Push Hook"},
		}
		contentType, eventID, err := detectContentTypeAndEventID(header)
		require.EqualError(t, err, "Issue with Content-Type Header: No value found in HEADER for the key: Content-Type")
		require.Equal(t, "", contentType)
		require.Equal(t, "", eventID)
	}
}

func Test_transformCodePushEvent(t *testing.T) {
	t.Log("Do Transform - single commit")
	{
		codePush := CodePushEventModel{
			ObjectKind:  "push",
			Ref:         "refs/heads/master",
			CheckoutSHA: "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
			Commits: []CommitModel{
				{
					CommitHash:    "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
					CommitMessage: `Response: omit the "failed_responses" array if empty`,
				},
			},
		}
		hookTransformResult := transformCodePushEvent(codePush)
		require.NoError(t, hookTransformResult.Error)
		require.False(t, hookTransformResult.ShouldSkip)
		require.Equal(t, []bitriseapi.TriggerAPIParamsModel{
			{
				BuildParams: bitriseapi.BuildParamsModel{
					CommitHash:    "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
					CommitMessage: `Response: omit the "failed_responses" array if empty`,
					Branch:        "master",
				},
			},
		}, hookTransformResult.TriggerAPIParams)
	}

	t.Log("Do Transform - multiple commits - CheckoutSHA match should trigger the build")
	{
		codePush := CodePushEventModel{
			ObjectKind:  "push",
			Ref:         "refs/heads/master",
			CheckoutSHA: "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
			Commits: []CommitModel{
				{
					CommitHash:    "7782203aaf0daabbd245ec0370c751eac6a4eb55",
					CommitMessage: `switch to three component versions`,
				},
				{
					CommitHash:    "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
					CommitMessage: `Response: omit the "failed_responses" array if empty`,
				},
				{
					CommitHash:    "ef77f9dba498f335e2e7078bdb52f9e11c214c58",
					CommitMessage: `get version : three component version`,
				},
			},
		}
		hookTransformResult := transformCodePushEvent(codePush)
		require.NoError(t, hookTransformResult.Error)
		require.False(t, hookTransformResult.ShouldSkip)
		require.Equal(t, []bitriseapi.TriggerAPIParamsModel{
			{
				BuildParams: bitriseapi.BuildParamsModel{
					CommitHash:    "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
					CommitMessage: `Response: omit the "failed_responses" array if empty`,
					Branch:        "master",
				},
			},
		}, hookTransformResult.TriggerAPIParams)
	}

	t.Log("No commit matches CheckoutSHA")
	{
		codePush := CodePushEventModel{
			ObjectKind:  "push",
			Ref:         "refs/heads/master",
			CheckoutSHA: "checkout-sha",
			Commits: []CommitModel{
				{
					CommitHash:    "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
					CommitMessage: `Response: omit the "failed_responses" array if empty`,
				},
			},
		}
		hookTransformResult := transformCodePushEvent(codePush)
		require.EqualError(t, hookTransformResult.Error, "The commit specified by 'checkout_sha' was not included in the 'commits' array - no match found")
		require.False(t, hookTransformResult.ShouldSkip)
		require.Nil(t, hookTransformResult.TriggerAPIParams)
	}

	t.Log("Not a head ref")
	{
		codePush := CodePushEventModel{
			ObjectKind:  "push",
			Ref:         "refs/not/head",
			CheckoutSHA: "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
			Commits: []CommitModel{
				{
					CommitHash:    "f8f37818dc89a67516adfc21896d0c9ec43d05c2",
					CommitMessage: `Response: omit the "failed_responses" array if empty`,
				},
			},
		}
		hookTransformResult := transformCodePushEvent(codePush)
		require.True(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "Ref (refs/not/head) is not a head ref")
		require.Nil(t, hookTransformResult.TriggerAPIParams)
	}
}

func Test_HookProvider_TransformRequest(t *testing.T) {
	provider := HookProvider{}

	const sampleCodePushData = `{
  "object_kind": "push",
  "ref": "refs/heads/develop",
  "checkout_sha": "1606d3dd4c4dc83ee8fed8d3cfd911da851bf740",
  "commits": [
    {
      "id": "29da60ce2c47a6696bc82f2e6ec4a075695eb7c3",
      "message": "first commit message"
    },
    {
      "id": "1606d3dd4c4dc83ee8fed8d3cfd911da851bf740",
      "message": "second commit message"
    }
  ]
}`

	t.Log("Code Push - should be handled")
	{
		request := http.Request{
			Header: http.Header{
				"X-Gitlab-Event": {"Push Hook"},
				"Content-Type":   {"application/json"},
			},
			Body: ioutil.NopCloser(strings.NewReader(sampleCodePushData)),
		}
		hookTransformResult := provider.TransformRequest(&request)
		require.NoError(t, hookTransformResult.Error)
		require.False(t, hookTransformResult.ShouldSkip)
		require.Equal(t, []bitriseapi.TriggerAPIParamsModel{
			{
				BuildParams: bitriseapi.BuildParamsModel{
					CommitHash:    "1606d3dd4c4dc83ee8fed8d3cfd911da851bf740",
					CommitMessage: "second commit message",
					Branch:        "develop",
				},
			},
		}, hookTransformResult.TriggerAPIParams)
	}

	t.Log("Unsuported Content-Type")
	{
		request := http.Request{
			Header: http.Header{
				"X-Gitlab-Event": {"Push Hook"},
				"Content-Type":   {"not/supported"},
			},
			Body: ioutil.NopCloser(strings.NewReader(sampleCodePushData)),
		}
		hookTransformResult := provider.TransformRequest(&request)
		require.False(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "Content-Type is not supported: not/supported")
	}

	t.Log("Unsupported event type - should error")
	{
		request := http.Request{
			Header: http.Header{
				"X-Gitlab-Event": {"Tag Push Hook"},
				"Content-Type":   {"application/json"},
			},
			Body: ioutil.NopCloser(strings.NewReader(sampleCodePushData)),
		}
		hookTransformResult := provider.TransformRequest(&request)
		require.False(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "Unsupported Webhook event: Tag Push Hook")
	}

	t.Log("No Request Body")
	{
		request := http.Request{
			Header: http.Header{
				"X-Gitlab-Event": {"Push Hook"},
				"Content-Type":   {"application/json"},
			},
		}
		hookTransformResult := provider.TransformRequest(&request)
		require.False(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "Failed to read content of request body: no or empty request body")
	}
}

func Test_transformMergeRequestEvent(t *testing.T) {
	t.Log("Unsupported Merge Request kind")
	{
		mergeRequest := MergeRequestEventModel{
			ObjectKind: "labeled",
		}
		hookTransformResult := transformMergeRequestEvent(mergeRequest)
		require.True(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "Not a Merge Request object")
	}

	t.Log("Empty Merge Request state")
	{
		mergeRequest := MergeRequestEventModel{
			ObjectKind:       "merge_request",
			ObjectAttributes: ObjectAttributesInfoModel{},
		}
		hookTransformResult := transformMergeRequestEvent(mergeRequest)
		require.True(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "No Merge Request state specified")
	}

	t.Log("Already Merged")
	{
		mergeRequest := MergeRequestEventModel{
			ObjectKind: "merge_request",
			ObjectAttributes: ObjectAttributesInfoModel{
				State:          "opened",
				MergeCommitSHA: "asd123",
			},
		}
		hookTransformResult := transformMergeRequestEvent(mergeRequest)
		require.True(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "Merge Request already merged")
	}

	t.Log("Merge error")
	{
		mergeRequest := MergeRequestEventModel{
			ObjectKind: "merge_request",
			ObjectAttributes: ObjectAttributesInfoModel{
				State:      "opened",
				MergeError: "Some merge error",
			},
		}
		hookTransformResult := transformMergeRequestEvent(mergeRequest)
		require.True(t, hookTransformResult.ShouldSkip)
		require.EqualError(t, hookTransformResult.Error, "Merge Request is not mergeable")
	}

	t.Log("Not yet merged")
	{
		mergeRequest := MergeRequestEventModel{
			ObjectKind: "merge_request",
			ObjectAttributes: ObjectAttributesInfoModel{
				ID:    12,
				Title: "PR test",
				State: "opened",
				Source: BranchInfoModel{
					VisibilityLevel: 20,
					GitSSHURL:       "git@github.com:bitrise-io/bitrise-webhooks.git",
					GitHTTPURL:      "https://gitlab.com/bitrise-io/bitrise-webhooks.git",
				},
				SourceBranch: "feature/gitlab-pr",
				Target: BranchInfoModel{
					VisibilityLevel: 20,
					GitSSHURL:       "git@github.com:bitrise-io/bitrise-webhooks.git",
					GitHTTPURL:      "https://gitlab.com/bitrise-io/bitrise-webhooks.git",
				},
				TargetBranch: "master",
				LastCommit: LastCommitInfoModel{
					SHA: "83b86e5f286f546dc5a4a58db66ceef44460c85e",
				},
			},
		}

		hookTransformResult := transformMergeRequestEvent(mergeRequest)
		require.NoError(t, hookTransformResult.Error)
		require.False(t, hookTransformResult.ShouldSkip)
		require.Equal(t, []bitriseapi.TriggerAPIParamsModel{
			{
				BuildParams: bitriseapi.BuildParamsModel{
					CommitHash:               "83b86e5f286f546dc5a4a58db66ceef44460c85e",
					CommitMessage:            "PR test",
					Branch:                   "feature/gitlab-pr",
					BranchDest:               "master",
					PullRequestID:            pointers.NewIntPtr(12),
					PullRequestRepositoryURL: "https://gitlab.com/bitrise-io/bitrise-webhooks.git",
					PullRequestHeadBranch:    "merge-requests/12/head",
				},
			},
		}, hookTransformResult.TriggerAPIParams)
	}

	t.Log("Pull Request - Title & Body")
	{
		mergeRequest := MergeRequestEventModel{
			ObjectKind: "merge_request",
			ObjectAttributes: ObjectAttributesInfoModel{
				ID:          12,
				Title:       "PR test",
				Description: "PR test body",
				State:       "opened",
				Source: BranchInfoModel{
					VisibilityLevel: 20,
					GitSSHURL:       "git@github.com:bitrise-io/bitrise-webhooks.git",
					GitHTTPURL:      "https://gitlab.com/bitrise-io/bitrise-webhooks.git",
				},
				SourceBranch: "feature/gitlab-pr",
				Target: BranchInfoModel{
					VisibilityLevel: 20,
					GitSSHURL:       "git@github.com:bitrise-io/bitrise-webhooks.git",
					GitHTTPURL:      "https://gitlab.com/bitrise-io/bitrise-webhooks.git",
				},
				TargetBranch: "master",
				LastCommit: LastCommitInfoModel{
					SHA: "83b86e5f286f546dc5a4a58db66ceef44460c85e",
				},
			},
		}

		hookTransformResult := transformMergeRequestEvent(mergeRequest)
		require.NoError(t, hookTransformResult.Error)
		require.False(t, hookTransformResult.ShouldSkip)
		require.Equal(t, []bitriseapi.TriggerAPIParamsModel{
			{
				BuildParams: bitriseapi.BuildParamsModel{
					CommitHash:               "83b86e5f286f546dc5a4a58db66ceef44460c85e",
					CommitMessage:            "PR test\n\nPR test body",
					Branch:                   "feature/gitlab-pr",
					BranchDest:               "master",
					PullRequestID:            pointers.NewIntPtr(12),
					PullRequestRepositoryURL: "https://gitlab.com/bitrise-io/bitrise-webhooks.git",
					PullRequestHeadBranch:    "merge-requests/12/head",
				},
			},
		}, hookTransformResult.TriggerAPIParams)
	}
}

func Test_isAcceptEventType(t *testing.T) {
	t.Log("Accept")
	{
		for _, anEvent := range []string{"Push Hook", "Merge Request Hook"} {
			t.Log(" * " + anEvent)
			require.Equal(t, true, isAcceptEventType(anEvent))
		}
	}

	t.Log("Don't accept")
	{
		for _, anEvent := range []string{"",
			"a", "not-an-action",
			"Issue Hook", "Note Hook", "Wiki Page Hook"} {
			t.Log(" * " + anEvent)
			require.Equal(t, false, isAcceptEventType(anEvent))
		}
	}
}

func Test_getRepositoryURL(t *testing.T) {
	t.Log("Visibility == 0")
	{
		branchInfoModel := BranchInfoModel{
			VisibilityLevel: 0,
			GitSSHURL:       "git@gitlab.com:test/test-repo.git",
			GitHTTPURL:      "https://gitlab.com/test/test-repo.git",
		}

		t.Log(fmt.Sprintf(" Visibility: %d", branchInfoModel.VisibilityLevel))
		require.Equal(t, "git@gitlab.com:test/test-repo.git", branchInfoModel.getRepositoryURL())
	}

	t.Log("Visibility == 10")
	{
		branchInfoModel := BranchInfoModel{
			VisibilityLevel: 10,
			GitSSHURL:       "git@gitlab.com:test/test-repo.git",
			GitHTTPURL:      "https://gitlab.com/test/test-repo.git",
		}

		t.Log(fmt.Sprintf(" Visibility: %d", branchInfoModel.VisibilityLevel))
		require.Equal(t, "git@gitlab.com:test/test-repo.git", branchInfoModel.getRepositoryURL())
	}

	t.Log("Visibility == 20")
	{
		branchInfoModel := BranchInfoModel{
			VisibilityLevel: 20,
			GitSSHURL:       "git@gitlab.com:test/test-repo.git",
			GitHTTPURL:      "https://gitlab.com/test/test-repo.git",
		}

		t.Log(fmt.Sprintf(" Visibility: %d", branchInfoModel.VisibilityLevel))
		require.Equal(t, "https://gitlab.com/test/test-repo.git", branchInfoModel.getRepositoryURL())
	}
}
