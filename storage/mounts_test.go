package storage

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/databrickslabs/terraform-provider-databricks/access"
	"github.com/databrickslabs/terraform-provider-databricks/compute"
	"github.com/databrickslabs/terraform-provider-databricks/internal"

	"github.com/databrickslabs/terraform-provider-databricks/qa"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"

	"github.com/databrickslabs/terraform-provider-databricks/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mountPointThroughReusedCluster(t *testing.T) (*common.DatabricksClient, MountPoint) {
	if _, ok := os.LookupEnv("CLOUD_ENV"); !ok {
		t.Skip("Acceptance tests skipped unless env 'CLOUD_ENV' is set")
	}
	ctx := context.Background()
	client := common.CommonEnvironmentClient()
	clusterInfo := compute.NewTinyClusterInCommonPoolPossiblyReused()
	randomName := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	return client, MountPoint{
		exec:      client.CommandExecutor(ctx),
		clusterID: clusterInfo.ClusterID,
		name:      randomName,
	}
}

func testWithNewSecretScope(t *testing.T, callback func(string, string),
	client *common.DatabricksClient, suffix, secret string) {
	randomScope := "test" + suffix
	randomKey := "key" + suffix

	ctx := context.Background()
	secretScopes := access.NewSecretScopesAPI(ctx, client)
	err := secretScopes.Create(access.SecretScope{
		Name:                   randomScope,
		InitialManagePrincipal: "users",
	})
	require.NoError(t, err)
	defer func() {
		err = secretScopes.Delete(randomScope)
		assert.NoError(t, err)
	}()

	secrets := access.NewSecretsAPI(ctx, client)
	err = secrets.Create(secret, randomScope, randomKey)
	require.NoError(t, err)

	callback(randomScope, randomKey)
}

func testMounting(t *testing.T, mp MountPoint, m Mount) {
	client := common.CommonEnvironmentClient()
	source, err := mp.Mount(m, client)
	assert.Equal(t, m.Source(), source)
	assert.NoError(t, err)
	defer func() {
		err = mp.Delete()
		assert.NoError(t, err)
	}()
	source, err = mp.Source()
	require.Equalf(t, m.Source(), source, "Error: %v", err)
}

func TestAccDeleteInvalidMountFails(t *testing.T) {
	_, mp := mountPointThroughReusedCluster(t)
	err := mp.Delete()
	assert.True(t, strings.Contains(err.Error(), "Directory not mounted"), err.Error())
}

func TestAccSourceOnInvalidMountFails(t *testing.T) {
	_, mp := mountPointThroughReusedCluster(t)
	source, err := mp.Source()
	assert.Equal(t, "", source)
	qa.AssertErrorStartsWith(t, err, "Mount not found")
}

func TestAccInvalidSecretScopeFails(t *testing.T) {
	_, mp := mountPointThroughReusedCluster(t)
	client := common.CommonEnvironmentClient()
	source, err := mp.Mount(AzureADLSGen1Mount{
		ClientID:        "abc",
		TenantID:        "bcd",
		PrefixType:      "dfs.adls",
		StorageResource: "def",
		Directory:       "/",
		SecretKey:       "key",
		SecretScope:     "y",
	}, client)
	assert.Equal(t, "", source)
	qa.AssertErrorStartsWith(t, err, "Secret does not exist with scope: y and key: key")
}

func TestValidateMountDirectory(t *testing.T) {
	testCases := []struct {
		directory  string
		errorCount int
	}{
		{"", 0},
		{"/directory", 0},
		{"directory", 1},
	}
	for _, tc := range testCases {
		_, errs := ValidateMountDirectory(tc.directory, "key")

		assert.Lenf(t, errs, tc.errorCount, "directory '%s' does not generate the expected error count", tc.directory)
	}
}

const expectedCommandResp = "done"

func testMountFuncHelper(t *testing.T, mountFunc func(mp MountPoint, mount Mount) (string, error), mount Mount,
	mountName, expectedCommand string) {
	c := common.DatabricksClient{
		Host:  ".",
		Token: ".",
	}
	err := c.Configure()
	assert.NoError(t, err)

	var called bool

	c.WithCommandMock(func(commandStr string) common.CommandResults {
		called = true
		assert.Equal(t, internal.TrimLeadingWhitespace(expectedCommand), internal.TrimLeadingWhitespace(commandStr))
		return common.CommandResults{
			ResultType: "text",
			Data:       expectedCommandResp,
		}
	})

	ctx := context.Background()
	mp := MountPoint{
		exec:      c.CommandExecutor(ctx),
		clusterID: "random_cluster_id",
		name:      mountName,
	}

	resp, err := mountFunc(mp, mount)
	assert.NoError(t, err)
	assert.True(t, called, "mocked command was not invoked")
	assert.Equal(t, expectedCommandResp, resp)
}

type mockMount struct{}

func (t mockMount) Source() string { return "fake-mount" }
func (t mockMount) Config(client *common.DatabricksClient) map[string]string {
	return map[string]string{"fake-key": "fake-value"}
}

func TestMountPoint_Mount(t *testing.T) {
	mount := mockMount{}
	expectedMountSource := "fake-mount"
	expectedMountConfig := `{"fake-key":"fake-value"}`
	mountName := "this_mount"
	expectedCommand := fmt.Sprintf(`
		def safe_mount(mount_point, mount_source, configs):
			for mount in dbutils.fs.mounts():
				if mount.mountPoint == mount_point and mount.source == mount_source:
					return
			try:
				dbutils.fs.mount(mount_source, mount_point, extra_configs=configs)
				dbutils.fs.refreshMounts()
				dbutils.fs.ls(mount_point)
				return mount_source
			except Exception as e:
				try:
					dbutils.fs.unmount(mount_point)
				except Exception as e2:
					print("Failed to unmount", e2)
				raise e
		mount_source = safe_mount("/mnt/%s", %q, %s)
		dbutils.notebook.exit(mount_source)
	`, mountName, expectedMountSource, expectedMountConfig)
	testMountFuncHelper(t, func(mp MountPoint, mount Mount) (s string, e error) {
		client := common.CommonEnvironmentClient()
		return mp.Mount(mount, client)
	}, mount, mountName, expectedCommand)
}

func TestMountPoint_Source(t *testing.T) {
	mountName := "this_mount"
	expectedCommand := fmt.Sprintf(`
		dbutils.fs.refreshMounts()
		for mount in dbutils.fs.mounts():
			if mount.mountPoint == "/mnt/%s":
				dbutils.notebook.exit(mount.source)
		raise Exception("Mount not found")
	`, mountName)
	testMountFuncHelper(t, func(mp MountPoint, mount Mount) (s string, e error) {
		return mp.Source()
	}, nil, mountName, expectedCommand)
}

func TestMountPoint_Delete(t *testing.T) {
	mountName := "this_mount"
	expectedCommand := fmt.Sprintf(`
		mount_point = "/mnt/%s"
		dbutils.fs.unmount(mount_point)
		dbutils.fs.refreshMounts()
		for mount in dbutils.fs.mounts():
			if mount.mountPoint == mount_point:
				raise Exception("Failed to unmount")
		dbutils.notebook.exit("success")
	`, mountName)
	testMountFuncHelper(t, func(mp MountPoint, mount Mount) (s string, e error) {
		return expectedCommandResp, mp.Delete()
	}, nil, mountName, expectedCommand)
}

func TestDeletedMountClusterRecreates(t *testing.T) {
	qa.HTTPFixturesApply(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Status:   404,
		},
		{
			Method:       "GET",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/list",
			Response:     map[string]interface{}{},
		},
		{
			Method:       "GET",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/spark-versions",
			Response: compute.SparkVersionsList{
				SparkVersions: []compute.SparkVersion{
					{
						Version:     "7.1.x-cpu-ml-scala2.12",
						Description: "7.1 ML (includes Apache Spark 3.0.0, Scala 2.12)",
					},
				},
			},
		},
		{
			Method:       "GET",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/list-node-types",
			Response: compute.NodeTypeList{
				NodeTypes: []compute.NodeType{
					{
						NodeTypeID:     "Standard_F4s",
						InstanceTypeID: "Standard_F4s",
						MemoryMB:       8192,
						NumCores:       4,
						NodeInstanceType: &compute.NodeInstanceType{
							LocalDisks:      1,
							InstanceTypeID:  "Standard_F4s",
							LocalDiskSizeGB: 16,
							LocalNVMeDisks:  0,
						},
					},
				},
			},
		},
		{
			Method:       "POST",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/create",
			ExpectedRequest: compute.Cluster{
				AutoterminationMinutes: 10,
				ClusterName:            "terraform-mount",
				NodeTypeID:             "Standard_F4s",
				SparkVersion:           "7.3.x-scala2.12",
				CustomTags: map[string]string{
					"ResourceClass": "SingleNode",
				},
				SparkConf: map[string]string{
					"spark.databricks.cluster.profile": "singleNode",
					"spark.master":                     "local[*]",
				},
			},
			Response: compute.ClusterID{
				ClusterID: "bcd",
			},
		},
		{
			Method:       "GET",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/get?cluster_id=bcd",
			Response: compute.ClusterInfo{
				ClusterID: "bcd",
				State:     "RUNNING",
				SparkConf: map[string]string{
					"spark.databricks.acl.dfAclsEnabled": "true",
					"spark.databricks.cluster.profile":   "singleNode",
				},
			},
		},
	}, func(ctx context.Context, client *common.DatabricksClient) {
		clusterID, err := getMountingClusterID(ctx, client, "abc")
		assert.NoError(t, err)
		assert.Equal(t, "bcd", clusterID)
	})
}
