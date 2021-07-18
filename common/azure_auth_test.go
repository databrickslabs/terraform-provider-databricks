package common

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzureAuth_resourceID(t *testing.T) {
	aa := AzureAuth{}
	assert.Equal(t, "", aa.resourceID())

	aa.ResourceID = "/subscriptions/a/resourceGroups/b"
	assert.Equal(t, "", aa.resourceID())

	aa.ResourceID = "/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c"
	assert.Equal(t, "/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c", aa.resourceID())
	assert.Equal(t, "a", aa.SubscriptionID)
	assert.Equal(t, "b", aa.ResourceGroup)
	assert.Equal(t, "c", aa.WorkspaceName)

	aa = AzureAuth{}
	aa.SubscriptionID = "a"
	assert.Equal(t, "", aa.resourceID())
	aa.ResourceGroup = "b"
	assert.Equal(t, "", aa.resourceID())
	aa.WorkspaceName = "c"
	assert.Equal(t, "/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c", aa.resourceID())
}

func TestAddSpManagementTokenVisitor(t *testing.T) {
	aa := AzureAuth{}
	r := httptest.NewRequest("GET", "/a/b/c", http.NoBody)
	err := aa.addSpManagementTokenVisitor(r, &autorest.BearerAuthorizer{})
	assert.EqualError(t, err, "token provider is nil")
}

func TestAddSpManagementTokenVisitor_Refreshed(t *testing.T) {
	defer CleanupEnvironment()()
	os.Setenv("PATH", "testdata:/bin")

	aa := AzureAuth{}
	r := httptest.NewRequest("GET", "/a/b/c", http.NoBody)
	rct := refreshableCliToken{
		lock:           &sync.RWMutex{},
		resource:       "x",
		refreshMinutes: 6,
	}
	err := aa.addSpManagementTokenVisitor(r, autorest.NewBearerAuthorizer(&rct))
	require.NoError(t, err)
	assert.Equal(t, "...", r.Header[http.CanonicalHeaderKey("X-Databricks-Azure-SP-Management-Token")][0])
}

func TestAddSpManagementTokenVisitor_RefreshedError(t *testing.T) {
	defer CleanupEnvironment()()
	os.Setenv("PATH", "testdata:/bin")
	os.Setenv("FAIL", "yes")

	aa := AzureAuth{}
	r := httptest.NewRequest("GET", "/a/b/c", http.NoBody)
	rct := refreshableCliToken{
		lock:           &sync.RWMutex{},
		resource:       "x",
		refreshMinutes: 6,
	}
	err := aa.addSpManagementTokenVisitor(r, autorest.NewBearerAuthorizer(&rct))
	require.EqualError(t, err, "cannot get access token: This is just a failing script.\n")

	err = aa.addSpManagementTokenVisitor(r, autorest.NewBasicAuthorizer("a", "b"))
	require.Error(t, err)
}

func TestGetClientSecretAuthorizer(t *testing.T) {
	aa := AzureAuth{}
	auth, err := aa.getClientSecretAuthorizer(AzureDatabricksResourceID)
	require.Nil(t, auth)
	require.EqualError(t, err, "parameter 'clientID' cannot be empty")

	aa.TenantID = "a"
	aa.ClientID = "b"
	aa.ClientSecret = "c"
	auth, err = aa.getClientSecretAuthorizer(AzureDatabricksResourceID)
	require.NotNil(t, auth)
	require.NoError(t, err)
}

func TestEnsureWorkspaceURL_CornerCases(t *testing.T) {
	aa := AzureAuth{}
	err := aa.ensureWorkspaceURL(context.Background(), nil)
	assert.EqualError(t, err, "DatabricksClient is not configured")

	aa.databricksClient = &DatabricksClient{}
	err = aa.ensureWorkspaceURL(context.Background(), nil)
	assert.EqualError(t, err, "somehow resource id is not set")

	aa = AzureAuth{
		Environment:      "xyz",
		SubscriptionID:   "a",
		ResourceGroup:    "b",
		WorkspaceName:    "c",
		databricksClient: &DatabricksClient{},
	}
	err = aa.ensureWorkspaceURL(context.Background(), nil)
	assert.EqualError(t, err, "autorest/azure: There is no cloud environment matching the name \"AZUREXYZCLOUD\"")
}

func TestAcquirePAT_CornerCases(t *testing.T) {
	aa := AzureAuth{}
	_, err := aa.acquirePAT(context.Background(), func(resource string) (autorest.Authorizer, error) {
		return &autorest.BearerAuthorizer{}, fmt.Errorf("test")
	})
	assert.EqualError(t, err, "test")

	_, err = aa.acquirePAT(context.Background(), func(resource string) (autorest.Authorizer, error) {
		return &autorest.BearerAuthorizer{}, nil
	})
	assert.EqualError(t, err, "DatabricksClient is not configured")

	aa.databricksClient = &DatabricksClient{}
	aa.temporaryPat = &tokenResponse{
		TokenValue: "...",
	}
	auth, rre := aa.acquirePAT(context.Background(), func(resource string) (autorest.Authorizer, error) {
		return &autorest.BearerAuthorizer{}, nil
	})
	assert.NoError(t, rre)
	assert.Equal(t, "...", auth.TokenValue)
}

func TestAzureAuth_ensureWorkspaceURL(t *testing.T) {
	aa := AzureAuth{}

	cnt := []int{0}
	var serverURL string
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(rw http.ResponseWriter, req *http.Request) {
			if req.RequestURI ==
				"/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c?api-version=2018-04-01" {
				_, err := rw.Write([]byte(fmt.Sprintf(`{"properties": {"workspaceUrl": "%s"}}`,
					strings.ReplaceAll(serverURL, "https://", ""))))
				assert.NoError(t, err)
				cnt[0]++
				return
			}
			assert.Fail(t, fmt.Sprintf("Received unexpected call: %s %s",
				req.Method, req.RequestURI))
		}))
	server.StartTLS()
	serverURL = server.URL
	defer server.Close()

	aa.ResourceID = "/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c"
	// resource management endpoints end with a trailing slash in url
	aa.azureManagementEndpoint = fmt.Sprintf("%s/", server.URL)

	client := DatabricksClient{InsecureSkipVerify: true}
	client.configureHTTPCLient()
	aa.databricksClient = &client
	client.AzureAuth = aa

	token := &adal.Token{
		AccessToken: "TestToken",
		Resource:    "https://azure.microsoft.com/",
		Type:        "Bearer",
	}
	authorizer := autorest.NewBearerAuthorizer(token)
	err := aa.ensureWorkspaceURL(context.Background(), authorizer)
	assert.NoError(t, err)

	err = aa.ensureWorkspaceURL(context.Background(), authorizer)
	assert.NoError(t, err)
	assert.Equal(t, 1, cnt[0],
		"Calls to Azure Management API must be done only once")
}

func TestAzureAuth_configureWithClientSecretPAT(t *testing.T) {
	aa := AzureAuth{}
	auth, err := aa.configureWithClientSecret()
	assert.Nil(t, auth)
	assert.NoError(t, err)

	aa.ResourceID = "/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c"
	auth, err = aa.configureWithClientSecret()
	assert.Nil(t, auth)
	assert.NoError(t, err)

	token := &adal.Token{
		AccessToken: "TestToken",
		Resource:    "https://azure.microsoft.com/",
		Type:        "Bearer",
	}
	aa.authorizer = autorest.NewBearerAuthorizer(token)
	aa.UsePATForSPN = true

	var serverURL string
	dummyPAT := "dapi234567"
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(rw http.ResponseWriter, req *http.Request) {
			if req.RequestURI ==
				"/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c?api-version=2018-04-01" {
				_, err := rw.Write([]byte(fmt.Sprintf(`{"properties": {"workspaceUrl": "%s"}}`,
					strings.ReplaceAll(serverURL, "https://", ""))))
				assert.NoError(t, err)
				return
			}
			if req.RequestURI == "/api/2.0/token/create" {
				assert.Equal(t, token.AccessToken, req.Header.Get("X-Databricks-Azure-SP-Management-Token"))
				assert.Equal(t, "Bearer "+token.AccessToken, req.Header.Get("Authorization"))
				assert.Equal(t, aa.ResourceID, req.Header.Get("X-Databricks-Azure-Workspace-Resource-Id"))
				_, err := rw.Write([]byte(fmt.Sprintf(`{
					"token_value": "%s", 
					"token_info": {
						"token_id": "qwertyu",
						"creation_time": 1234567,
						"expiry_time": 1234568
					}
				}`, dummyPAT)))
				assert.NoError(t, err)
				return
			}
			if req.RequestURI == "/api/2.0/clusters/list-zones" {
				assert.Equal(t, "Bearer "+dummyPAT, req.Header.Get("Authorization"))
				_, err := rw.Write([]byte(`{"zones": ["a", "b", "c"]}`))
				assert.NoError(t, err)
				return
			}
			assert.Fail(t, fmt.Sprintf("Received unexpected call: %s %s",
				req.Method, req.RequestURI))
		}))
	server.StartTLS()
	serverURL = server.URL
	defer server.Close()

	aa.ClientID = "a"
	aa.ClientSecret = "b"
	aa.TenantID = "c"
	// resource management endpoints end with a trailing slash in url
	aa.azureManagementEndpoint = fmt.Sprintf("%s/", server.URL)
	auth, err = aa.configureWithClientSecret()
	assert.NotNil(t, auth)
	assert.NoError(t, err)

	client := DatabricksClient{InsecureSkipVerify: true}
	client.authVisitor = auth
	err = client.Configure()
	assert.NoError(t, err)
	aa.databricksClient = &client
	client.AzureAuth = aa

	type ZonesInfo struct {
		Zones       []string `json:"zones,omitempty"`
		DefaultZone string   `json:"default_zone,omitempty"`
	}
	var zi ZonesInfo
	err = client.Get(context.Background(), "/clusters/list-zones", nil, &zi)
	assert.NotNil(t, zi)
	assert.NoError(t, err)
	assert.Len(t, zi.Zones, 3)
}

func TestAzureAuth_configureWithClientSecretAAD(t *testing.T) {
	aa := AzureAuth{}
	aa.ResourceID = "/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c"

	token := &adal.Token{
		AccessToken: "TestToken",
		Resource:    "https://azure.microsoft.com/",
		Type:        "Bearer",
	}
	aa.authorizer = autorest.NewBearerAuthorizer(token)

	var serverURL string
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(rw http.ResponseWriter, req *http.Request) {
			if req.RequestURI ==
				"/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c?api-version=2018-04-01" {
				_, err := rw.Write([]byte(fmt.Sprintf(`{"properties": {"workspaceUrl": "%s"}}`,
					strings.ReplaceAll(serverURL, "https://", ""))))
				assert.NoError(t, err)
				return
			}
			if req.RequestURI == "/api/2.0/clusters/list-zones" {
				assert.Equal(t, token.AccessToken, req.Header.Get("X-Databricks-Azure-SP-Management-Token"))
				assert.Equal(t, "Bearer "+token.AccessToken, req.Header.Get("Authorization"))
				assert.Equal(t, aa.ResourceID, req.Header.Get("X-Databricks-Azure-Workspace-Resource-Id"))
				_, err := rw.Write([]byte(`{"zones": ["a", "b", "c"]}`))
				assert.NoError(t, err)
				return
			}
			assert.Fail(t, fmt.Sprintf("Received unexpected call: %s %s",
				req.Method, req.RequestURI))
		}))
	server.StartTLS()
	serverURL = server.URL
	defer server.Close()

	aa.ClientID = "a"
	aa.ClientSecret = "b"
	aa.TenantID = "c"
	aa.databricksClient = &DatabricksClient{InsecureSkipVerify: true}
	// resource management endpoints end with a trailing slash in url
	aa.azureManagementEndpoint = fmt.Sprintf("%s/", server.URL)
	auth, err := aa.configureWithClientSecret()
	assert.NoError(t, err)

	client := DatabricksClient{InsecureSkipVerify: true}
	client.authVisitor = auth
	aa.databricksClient = &client
	err = client.Configure()
	assert.NoError(t, err)
	aa.databricksClient = &client
	client.AzureAuth = aa

	type ZonesInfo struct {
		Zones       []string `json:"zones,omitempty"`
		DefaultZone string   `json:"default_zone,omitempty"`
	}
	var zi ZonesInfo
	err = client.Get(context.Background(), "/clusters/list-zones", nil, &zi)
	assert.NotNil(t, zi)
	assert.NoError(t, err)
	assert.Len(t, zi.Zones, 3)
}

func TestAzureEnvironment_WithAzureManagementEndpoint(t *testing.T) {
	fakeEndpoint := "http://google.com"
	aa := AzureAuth{azureManagementEndpoint: fakeEndpoint}
	env, err := aa.GetAzureEnvironment()
	assert.Nil(t, err)
	// This value should be populated with azureManagementEndpoint for testing
	assert.Equal(t, env.ResourceManagerEndpoint, fakeEndpoint)
	// The rest should be nill
	assert.Equal(t, env.ActiveDirectoryEndpoint, "")

	// Making the azureManagementEndpoint empty should yield PublicCloud
	aa.azureManagementEndpoint = ""
	env, err = aa.GetAzureEnvironment()
	assert.Nil(t, err)
	assert.Equal(t, azure.PublicCloud, env)
}

func TestAzureEnvironment(t *testing.T) {
	aa := AzureAuth{}
	env, err := aa.GetAzureEnvironment()

	assert.Nil(t, err)
	assert.Equal(t, azure.PublicCloud, env)

	aa.Environment = "public"
	env, err = aa.GetAzureEnvironment()
	assert.Nil(t, err)
	assert.Equal(t, azure.PublicCloud, env)

	aa.Environment = "china"
	env, err = aa.GetAzureEnvironment()
	assert.Nil(t, err)
	assert.Equal(t, azure.ChinaCloud, env)

	aa.Environment = "german"
	env, err = aa.GetAzureEnvironment()
	assert.Nil(t, err)
	assert.Equal(t, azure.GermanCloud, env)

	aa.Environment = "usgovernment"
	env, err = aa.GetAzureEnvironment()
	assert.Nil(t, err)
	assert.Equal(t, azure.USGovernmentCloud, env)

	aa.Environment = "xyzdummy"
	_, err = aa.GetAzureEnvironment()
	assert.NotNil(t, err)
}

func TestInvalidAzureEnvironment(t *testing.T) {
	aa := AzureAuth{}

	aa.Environment = "xyzdummy"
	_, envErr := aa.GetAzureEnvironment()
	assert.NotNil(t, envErr)

	mockFunc := func(resource string) (autorest.Authorizer, error) {
		return nil, nil
	}

	ctx := context.Background()
	_, err := aa.simpleAADRequestVisitor(ctx, mockFunc)

	assert.Equal(t, envErr, err)

	_, err = aa.acquirePAT(ctx, mockFunc)
	assert.Equal(t, envErr, err)

	_, err = aa.getClientSecretAuthorizer("")
	assert.Equal(t, envErr, err)
}

func TestMaybeExtendError(t *testing.T) {
	err := fmt.Errorf("Some test")
	err2 := maybeExtendAuthzError(err)

	assert.EqualError(t, err2, err.Error())

	msg := "Azure authorization error. Does your SPN"

	err = fmt.Errorf("something does not have authorization to perform action abc")
	err2 = maybeExtendAuthzError(err)
	assert.True(t, strings.HasPrefix(err2.Error(), msg), err2.Error())

	err = APIError{StatusCode: 403}
	err2 = maybeExtendAuthzError(err)
	assert.True(t, strings.HasPrefix(err2.Error(), msg), err2.Error())
}
