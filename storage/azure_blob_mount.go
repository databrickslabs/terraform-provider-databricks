package storage

import (
	"fmt"

	"github.com/databrickslabs/terraform-provider-databricks/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// AzureBlobMount describes the object for a azure blob storage mount - a.k.a. NativeAzureFileSystem
type AzureBlobMount struct {
	ContainerName      string `json:"container_name"`
	StorageAccountName string `json:"storage_account_name"`
	Directory          string `json:"directory"`
	AuthType           string `json:"auth_type"`
	SecretScope        string `json:"token_secret_scope"`
	SecretKey          string `json:"token_secret_key"`
}

// Source ...
func (m AzureBlobMount) Source() string {
	return fmt.Sprintf("wasbs://%[1]s@%[2]s.blob.core.windows.net%[3]s",
		m.ContainerName, m.StorageAccountName, m.Directory)
}

// Config ...
func (m AzureBlobMount) Config(client *common.DatabricksClient) map[string]string {
	var confKey string
	if m.AuthType == "SAS" {
		confKey = fmt.Sprintf("fs.azure.sas.%s.%s.blob.core.windows.net", m.ContainerName, m.StorageAccountName)
	} else {
		confKey = fmt.Sprintf("fs.azure.account.key.%s.blob.core.windows.net", m.StorageAccountName)
	}
	return map[string]string{
		confKey: fmt.Sprintf("{secrets/%s/%s}", m.SecretScope, m.SecretKey),
	}
}

// ResourceAzureBlobMount creates the resource
func ResourceAzureBlobMount() *schema.Resource {
	return commonMountResource(AzureBlobMount{}, map[string]*schema.Schema{
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
		"container_name": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"storage_account_name": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"directory": {
			Type:         schema.TypeString,
			Optional:     true,
			Default:      "/",
			ValidateFunc: ValidateMountDirectory,
			ForceNew:     true,
		},
		"auth_type": {
			Type:         schema.TypeString,
			Required:     true,
			ValidateFunc: validation.StringInSlice([]string{"SAS", "ACCESS_KEY"}, false),
			ForceNew:     true,
		},
		"token_secret_scope": {
			Type:     schema.TypeString,
			Required: true,
			ForceNew: true,
		},
		"token_secret_key": {
			Type:      schema.TypeString,
			Required:  true,
			Sensitive: true,
			ForceNew:  true,
		},
	})
}
