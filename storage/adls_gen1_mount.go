package storage

import (
	"fmt"

	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/databrickslabs/terraform-provider-databricks/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// AzureADLSGen1Mount describes the object for a azure datalake gen 1 storage mount
type AzureADLSGen1Mount struct {
	StorageResource string `json:"storage_resource_name"`
	Directory       string `json:"directory,omitempty"`
	PrefixType      string `json:"spark_conf_prefix"`
	ClientID        string `json:"client_id"`
	TenantID        string `json:"tenant_id"`
	SecretScope     string `json:"client_secret_scope"`
	SecretKey       string `json:"client_secret_key"`
}

// Source ...
func (m AzureADLSGen1Mount) Source() string {
	return fmt.Sprintf("adl://%s.azuredatalakestore.net%s", m.StorageResource, m.Directory)
}

// Config ...
func (m AzureADLSGen1Mount) Config(client *common.DatabricksClient) map[string]string {
	azureEnvironment, err := client.AzureAuth.GetAzureEnvironment()
	var clientEndpoint string
	if err != nil {
		clientEndpoint = azure.PublicCloud.ActiveDirectoryEndpoint
	} else {
		clientEndpoint = azureEnvironment.ActiveDirectoryEndpoint
	}
	return map[string]string{
		m.PrefixType + ".oauth2.access.token.provider.type": "ClientCredential",

		m.PrefixType + ".oauth2.client.id":   m.ClientID,
		m.PrefixType + ".oauth2.credential":  fmt.Sprintf("{secrets/%s/%s}", m.SecretScope, m.SecretKey),
		m.PrefixType + ".oauth2.refresh.url": fmt.Sprintf("%s/%s/oauth2/token", clientEndpoint, m.TenantID),
	}
}

// ResourceAzureAdlsGen1Mount creates the resource
func ResourceAzureAdlsGen1Mount() *schema.Resource {
	return commonMountResource(AzureADLSGen1Mount{}, map[string]*schema.Schema{
		"cluster_id": {
			Type:     schema.TypeString,
			Optional: true,
			ForceNew: true,
		},
		"source": {
			Type:     schema.TypeString,
			Computed: true,
		},
		"mount_name": {
			// TODO: have it by default as storage_resource_name
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},

		"storage_resource_name": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"spark_conf_prefix": {
			Type:         schema.TypeString,
			Optional:     true,
			Default:      "fs.adl",
			ValidateFunc: validation.StringInSlice([]string{"fs.adl", "dfs.adls"}, false),
			ForceNew:     true,
		},
		"directory": {
			Type:     schema.TypeString,
			Optional: true,
			Computed: true,
			//Default:  "/",
			ForceNew:     true,
			ValidateFunc: ValidateMountDirectory,
		},

		"tenant_id": {
			// TODO: take it from AzureAuth if not speficied
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"client_id": {
			// TODO: take it from AzureAuth if not speficied
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"client_secret_scope": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"client_secret_key": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
	})
}
