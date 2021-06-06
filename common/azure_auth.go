package common

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
)

// List of management information
const (
	AzureDatabricksResourceID string = "2ff814a6-3304-4ab8-85cb-cd0e6f879c1d"
)

// AzureAuth contains all the auth information for azure sp authentication
type AzureAuth struct {
	WorkspaceName  string
	ResourceGroup  string
	SubscriptionID string

	// azurerm_databricks_workspace.this.id
	ResourceID string

	ClientSecret string
	ClientID     string
	TenantID     string
	Environment  string

	// temporary workaround for SP-based auth
	PATTokenDurationSeconds string
	UsePATForCLI            bool

	// private property to give resource access
	databricksClient *DatabricksClient

	azureManagementEndpoint string
	authorizer              autorest.Authorizer
	temporaryPat            *tokenResponse
}

type tokenRequest struct {
	LifetimeSeconds int64  `json:"lifetime_seconds,omitempty"`
	Comment         string `json:"comment,omitempty"`
}

type tokenResponse struct {
	TokenValue string     `json:"token_value,omitempty"`
	TokenInfo  *tokenInfo `json:"token_info,omitempty"`
}

// tokenInfo is a struct that contains metadata about a given token
type tokenInfo struct {
	TokenID      string `json:"token_id,omitempty"`
	CreationTime int64  `json:"creation_time,omitempty"`
	ExpiryTime   int64  `json:"expiry_time,omitempty"`
	Comment      string `json:"comment,omitempty"`
}

var authorizerMutex sync.Mutex

func (aa *AzureAuth) GetAzureEnvironment() (azure.Environment, error) {
	// Used for unit testing purposes
	if aa.azureManagementEndpoint != "" {
		return azure.Environment{
			ResourceManagerEndpoint: aa.azureManagementEndpoint,
		}, nil
	}

	if aa.Environment == "" {
		return azure.PublicCloud, nil
	}

	envName := fmt.Sprintf("AZURE%sCLOUD", strings.ToUpper(aa.Environment))
	return azure.EnvironmentFromName(envName)
}

func (aa *AzureAuth) resourceID() string {
	if aa.ResourceID != "" {
		if aa.SubscriptionID == "" {
			res, err := azure.ParseResourceID(aa.ResourceID)
			if err != nil {
				log.Printf("[ERROR] %s", err)
				return ""
			}
			aa.SubscriptionID = res.SubscriptionID
			aa.ResourceGroup = res.ResourceGroup
			aa.WorkspaceName = res.ResourceName
		}
		return aa.ResourceID
	}
	if aa.SubscriptionID == "" || aa.ResourceGroup == "" || aa.WorkspaceName == "" {
		return ""
	}
	r := azure.Resource{
		SubscriptionID: aa.SubscriptionID,
		ResourceGroup:  aa.ResourceGroup,
		Provider:       "Microsoft.Databricks",
		ResourceType:   "workspaces",
		ResourceName:   aa.WorkspaceName,
	}
	aa.ResourceID = r.String()
	return aa.ResourceID
}

// IsClientSecretSet returns true if client id/secret and tenand id are supplied
func (aa *AzureAuth) IsClientSecretSet() bool {
	return aa.ClientID != "" && aa.ClientSecret != "" && aa.TenantID != ""
}

func (aa *AzureAuth) configureWithClientSecret() (func(r *http.Request) error, error) {
	if aa.databricksClient != nil && !aa.databricksClient.IsAzure() {
		return nil, nil
	}
	if !aa.IsClientSecretSet() {
		return nil, nil
	}
	log.Printf("[INFO] Using Azure Service Principal client secret authentication")
	// return aa.simpleAADRequestVisitor(aa.getClientSecretAuthorizer, aa.addSpManagementTokenVisitor)
	return func(r *http.Request) error {
		pat, err := aa.acquirePAT(r.Context(), aa.getClientSecretAuthorizer, aa.addSpManagementTokenVisitor)
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", pat.TokenValue))
		return nil
	}, nil
}

func (aa *AzureAuth) addSpManagementTokenVisitor(r *http.Request, management autorest.Authorizer) error {
	log.Printf("[DEBUG] Setting 'X-Databricks-Azure-SP-Management-Token' header")
	ba, ok := management.(*autorest.BearerAuthorizer)
	if !ok {
		return fmt.Errorf("supposed to get BearerAuthorizer, but got %#v", management)
	}
	tokenProvider := ba.TokenProvider()
	if tokenProvider == nil {
		return fmt.Errorf("token provider is nil")
	}
	accessToken := tokenProvider.OAuthToken()
	if accessToken == "" {
		// DATABRICKS_HOST was provided, so request to Management API is not made,
		// therefore we manually need to ensure token refresh here
		var err error
		switch rf := tokenProvider.(type) {
		case adal.RefresherWithContext:
			err = rf.EnsureFreshWithContext(r.Context())
		case adal.Refresher:
			err = rf.EnsureFresh()
		}
		if err != nil {
			return err
		}
		accessToken = tokenProvider.OAuthToken()
	}
	r.Header.Set("X-Databricks-Azure-SP-Management-Token", accessToken)
	return nil
}

// go nolint
func (aa *AzureAuth) simpleAADRequestVisitor(
	ctx context.Context,
	authorizerFactory func(resource string) (autorest.Authorizer, error),
	visitors ...func(r *http.Request, ma autorest.Authorizer) error) (func(r *http.Request) error, error) {
	env, err := aa.GetAzureEnvironment()
	if err != nil {
		return nil, err
	}
	managementAuthorizer, err := authorizerFactory(env.ServiceManagementEndpoint)
	if err != nil {
		return nil, err
	}
	err = aa.ensureWorkspaceURL(ctx, managementAuthorizer)
	if err != nil {
		return nil, err
	}
	platformAuthorizer, err := authorizerFactory(AzureDatabricksResourceID)
	if err != nil {
		return nil, err
	}
	return func(r *http.Request) error {
		if len(visitors) > 0 {
			err = visitors[0](r, managementAuthorizer)
			if err != nil {
				return err
			}
		}
		resourceID := aa.resourceID()
		if resourceID != "" {
			r.Header.Set("X-Databricks-Azure-Workspace-Resource-Id", resourceID)
		}
		_, err = autorest.Prepare(r, platformAuthorizer.WithAuthorization())
		if err != nil {
			return err
		}
		return nil
	}, nil
}

func (aa *AzureAuth) acquirePAT(
	ctx context.Context,
	factory func(resource string) (autorest.Authorizer, error),
	visitors ...func(r *http.Request, ma autorest.Authorizer) error) (*tokenResponse, error) {
	if aa.temporaryPat != nil {
		// todo: add IsExpired
		return aa.temporaryPat, nil
	}
	authorizerMutex.Lock()
	defer authorizerMutex.Unlock()
	if aa.temporaryPat != nil {
		return aa.temporaryPat, nil
	}
	env, err := aa.GetAzureEnvironment()
	if err != nil {
		return nil, err
	}
	management, err := factory(env.ServiceManagementEndpoint)
	if err != nil {
		return nil, err
	}
	err = aa.ensureWorkspaceURL(ctx, management)
	if err != nil {
		return nil, err
	}
	token, err := aa.createPAT(ctx, func(r *http.Request) error {
		if len(visitors) > 0 {
			err = visitors[0](r, management)
			if err != nil {
				return err
			}
		}
		platform, err := factory(AzureDatabricksResourceID)
		if err != nil {
			return err
		}
		resourceID := aa.resourceID()
		if resourceID != "" {
			r.Header.Set("X-Databricks-Azure-Workspace-Resource-Id", resourceID)
		}
		_, err = autorest.Prepare(r, platform.WithAuthorization())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	aa.temporaryPat = &token
	return aa.temporaryPat, nil
}

func (aa *AzureAuth) patRequest() tokenRequest {
	seconds, err := strconv.ParseInt(aa.PATTokenDurationSeconds, 10, 64)
	if err != nil {
		seconds = 60 * 60
	}
	return tokenRequest{
		LifetimeSeconds: seconds,
		Comment:         "Secret made via Terraform",
	}
}

func (aa *AzureAuth) ensureWorkspaceURL(ctx context.Context,
	managementAuthorizer autorest.Authorizer) error {
	if aa.databricksClient == nil {
		return fmt.Errorf("DatabricksClient is not configured")
	}
	if aa.databricksClient.Host != "" {
		return nil
	}
	resourceID := aa.resourceID()
	if resourceID == "" {
		return fmt.Errorf("somehow resource id is not set")
	}
	log.Println("[DEBUG] Getting Workspace ID via management token.")
	env, err := aa.GetAzureEnvironment()
	if err != nil {
		return err
	}
	// All azure endpoints typically end with a trailing slash removing it because resourceID starts with slash
	managementResourceURL := strings.TrimSuffix(env.ResourceManagerEndpoint, "/") + resourceID
	var workspace azureDatabricksWorkspace
	resp, err := aa.databricksClient.genericQuery(ctx, http.MethodGet,
		managementResourceURL,
		map[string]string{
			"api-version": "2018-04-01",
		}, func(r *http.Request) error {
			_, err := autorest.Prepare(r, managementAuthorizer.WithAuthorization())
			if err != nil {
				return err
			}
			return nil
		})
	if err != nil {
		return err
	}
	err = json.Unmarshal(resp, &workspace)
	if err != nil {
		return err
	}
	aa.databricksClient.Host = fmt.Sprintf("https://%s/", workspace.Properties.WorkspaceURL)
	return nil
}

func (aa *AzureAuth) createPAT(ctx context.Context,
	interceptor func(r *http.Request) error) (tr tokenResponse, err error) {
	log.Println("[DEBUG] Creating workspace token")
	url := fmt.Sprintf("%sapi/2.0/token/create", aa.databricksClient.Host)
	body, err := aa.databricksClient.genericQuery(ctx,
		http.MethodPost, url, aa.patRequest(), interceptor)
	if err != nil {
		return
	}
	err = aa.databricksClient.unmarshall("/api/2.0/token/create", body, &tr)
	return
}

func (aa *AzureAuth) getClientSecretAuthorizer(resource string) (autorest.Authorizer, error) {
	if aa.authorizer != nil {
		// todo: probably should be two different ones...
		return aa.authorizer, nil
	}
	env, err := aa.GetAzureEnvironment()
	if err != nil {
		return nil, err
	}
	if resource != AzureDatabricksResourceID {
		es := auth.EnvironmentSettings{
			Values: map[string]string{
				auth.ClientID:     aa.ClientID,
				auth.ClientSecret: aa.ClientSecret,
				auth.TenantID:     aa.TenantID,
				auth.Resource:     resource,
			},
			Environment: env,
		}
		return es.GetAuthorizer()
	}
	platformTokenOAuthCfg, err := adal.NewOAuthConfigWithAPIVersion(
		env.ActiveDirectoryEndpoint,
		aa.TenantID,
		nil)
	if err != nil {
		return nil, err
	}
	spt, err := adal.NewServicePrincipalToken(
		*platformTokenOAuthCfg,
		aa.ClientID,
		aa.ClientSecret,
		AzureDatabricksResourceID)
	if err != nil {
		return nil, err
	}
	return autorest.NewBearerAuthorizer(spt), nil
}

type azureDatabricksWorkspace struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Type string `json:"type"`
	Sku  struct {
		Name string `json:"name"`
	} `json:"sku"`
	Location   string `json:"location"`
	Properties struct {
		ManagedResourceGroupID string      `json:"managedResourceGroupId"`
		Parameters             interface{} `json:"parameters"`
		ProvisioningState      string      `json:"provisioningState"`
		UIDefinitionURI        string      `json:"uiDefinitionUri"`
		Authorizations         []struct {
			PrincipalID      string `json:"principalId"`
			RoleDefinitionID string `json:"roleDefinitionId"`
		} `json:"authorizations"`
		CreatedBy struct {
			Oid           string `json:"oid"`
			Puid          string `json:"puid"`
			ApplicationID string `json:"applicationId"`
		} `json:"createdBy"`
		UpdatedBy struct {
			Oid           string `json:"oid"`
			Puid          string `json:"puid"`
			ApplicationID string `json:"applicationId"`
		} `json:"updatedBy"`
		CreatedDateTime time.Time `json:"createdDateTime"`
		WorkspaceID     string    `json:"workspaceId"`
		WorkspaceURL    string    `json:"workspaceUrl"`
	} `json:"properties"`
}
