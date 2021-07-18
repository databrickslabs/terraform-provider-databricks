package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strings"

	"github.com/databrickslabs/terraform-provider-databricks/common"
	"github.com/databrickslabs/terraform-provider-databricks/compute"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Mount exposes generic url & extra config map options
type Mount interface {
	Source() string
	Config(client *common.DatabricksClient) map[string]string
}

// MountPoint is something actionable
type MountPoint struct {
	exec      common.CommandExecutor
	clusterID string
	name      string
}

// Source returns mountpoint source
func (mp MountPoint) Source() (string, error) {
	result := mp.exec.Execute(mp.clusterID, "python", fmt.Sprintf(`
		dbutils.fs.refreshMounts()
		for mount in dbutils.fs.mounts():
			if mount.mountPoint == "/mnt/%s":
				dbutils.notebook.exit(mount.source)
		raise Exception("Mount not found")
	`, mp.name))
	return result.Text(), result.Err()
}

// Delete removes mount from workspace
func (mp MountPoint) Delete() error {
	result := mp.exec.Execute(mp.clusterID, "python", fmt.Sprintf(`
		mount_point = "/mnt/%s"
		dbutils.fs.unmount(mount_point)
		dbutils.fs.refreshMounts()
		for mount in dbutils.fs.mounts():
			if mount.mountPoint == mount_point:
				raise Exception("Failed to unmount")
		dbutils.notebook.exit("success")
	`, mp.name))
	return result.Err()
}

// Mount mounts object store on workspace
func (mp MountPoint) Mount(mo Mount, client *common.DatabricksClient) (source string, err error) {
	extraConfigs, err := json.Marshal(mo.Config(client))
	if err != nil {
		return
	}
	b := regexp.MustCompile(`"\{secrets/([^/]+)/([^\}]+)\}"`)
	extraConfigs = b.ReplaceAll(extraConfigs, []byte(`dbutils.secrets.get("$1", "$2")`))
	command := fmt.Sprintf(`
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
		mount_source = safe_mount("/mnt/%s", "%v", %s)
		dbutils.notebook.exit(mount_source)
	`, mp.name, mo.Source(), extraConfigs)
	result := mp.exec.Execute(mp.clusterID, "python", command)
	return result.Text(), result.Err()
}

func commonMountResource(tpl Mount, s map[string]*schema.Schema) *schema.Resource {
	resource := &schema.Resource{Schema: s, SchemaVersion: 2}
	// nolint should be a bigger context-aware refactor
	resource.CreateContext = mountCreate(tpl, resource)
	resource.ReadContext = mountRead(tpl, resource)
	resource.DeleteContext = mountDelete(tpl, resource)
	resource.Importer = &schema.ResourceImporter{
		StateContext: schema.ImportStatePassthroughContext,
	}
	return resource
}

// NewMountPoint returns new mount point config
func NewMountPoint(executor common.CommandExecutor, name, clusterID string) MountPoint {
	return MountPoint{
		exec:      executor,
		clusterID: clusterID,
		name:      name,
	}
}

func getOrCreateMountingCluster(clustersAPI compute.ClustersAPI) (string, error) {
	cluster, err := clustersAPI.GetOrCreateRunningCluster("terraform-mount", compute.Cluster{
		NumWorkers:  0,
		ClusterName: "terraform-mount",
		SparkVersion: clustersAPI.LatestSparkVersionOrDefault(
			compute.SparkVersionRequest{
				Latest:          true,
				LongTermSupport: true,
			}),
		NodeTypeID: clustersAPI.GetSmallestNodeType(
			compute.NodeTypeRequest{
				LocalDisk: true,
			}),
		AutoterminationMinutes: 10,
		SparkConf: map[string]string{
			"spark.master":                     "local[*]",
			"spark.databricks.cluster.profile": "singleNode",
		},
		CustomTags: map[string]string{
			"ResourceClass": "SingleNode",
		},
	})
	if err != nil {
		return "", err
	}
	return cluster.ClusterID, nil
}

func getMountingClusterID(ctx context.Context, client *common.DatabricksClient, clusterID string) (string, error) {
	clustersAPI := compute.NewClustersAPI(ctx, client)
	if clusterID == "" {
		return getOrCreateMountingCluster(clustersAPI)
	}
	clusterInfo, err := clustersAPI.Get(clusterID)
	if e, ok := err.(common.APIError); ok && e.IsMissing() {
		return getOrCreateMountingCluster(clustersAPI)
	}
	if err != nil {
		return "", err
	}
	if !clusterInfo.IsRunningOrResizing() {
		err = clustersAPI.Start(clusterInfo.ClusterID)
		if err != nil {
			return "", err
		}
	}
	return clusterID, nil
}

func mountCluster(ctx context.Context, tpl interface{}, d *schema.ResourceData,
	m interface{}, r *schema.Resource) (Mount, MountPoint, error) {
	var mountPoint MountPoint
	var mountConfig Mount

	client := m.(*common.DatabricksClient)
	mountPoint.exec = client.CommandExecutor(ctx)

	clusterID := d.Get("cluster_id").(string)
	clusterID, err := getMountingClusterID(ctx, client, clusterID)
	if err != nil {
		return mountConfig, mountPoint, err
	}
	mountPoint.clusterID = clusterID

	mountType := reflect.TypeOf(tpl)
	mountTypePointer := reflect.New(mountType)
	mountReflectValue := mountTypePointer.Elem()
	err = common.DataToReflectValue(d, r, mountReflectValue)
	if err != nil {
		return mountConfig, mountPoint, err
	}
	mountInterface := mountReflectValue.Interface()
	mountConfig = mountInterface.(Mount)

	name := d.Get("mount_name").(string)
	mountPoint.name = name
	d.SetId(name)

	return mountConfig, mountPoint, nil
}

// returns resource create mount for object store on workspace
func mountCreate(tpl interface{}, r *schema.Resource) func(context.Context, *schema.ResourceData, interface{}) diag.Diagnostics {
	return func(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
		mountConfig, mountPoint, err := mountCluster(ctx, tpl, d, m, r)
		if err != nil {
			return diag.FromErr(err)
		}
		client := m.(*common.DatabricksClient)
		log.Printf("[INFO] Mounting %s at /mnt/%s", mountConfig.Source(), d.Id())
		source, err := mountPoint.Mount(mountConfig, client)
		if err != nil {
			return diag.FromErr(err)
		}
		err = d.Set("source", source)
		if err != nil {
			return diag.FromErr(err)
		}
		return readMountSource(ctx, mountPoint, d)
	}
}

// reads and sets source of the mount
func readMountSource(ctx context.Context, mp MountPoint, d *schema.ResourceData) diag.Diagnostics {
	source, err := mp.Source()
	if err != nil {
		if err.Error() == "Mount not found" {
			log.Printf("[INFO] /mnt/%s is not mounted", d.Id())
			d.SetId("")
			return nil
		}
		return diag.FromErr(err)
	}
	if err = d.Set("source", source); err != nil {
		return diag.FromErr(err)
	}
	return nil
}

// return resource reader function
func mountRead(tpl Mount, r *schema.Resource) schema.ReadContextFunc {
	return func(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
		_, mp, err := mountCluster(ctx, tpl, d, m, r)
		if err != nil {
			return diag.FromErr(err)
		}
		return readMountSource(ctx, mp, d)
	}
}

// returns delete resource function
func mountDelete(tpl Mount, r *schema.Resource) schema.DeleteContextFunc {
	return func(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
		_, mp, err := mountCluster(ctx, tpl, d, m, r)
		if err != nil {
			return diag.FromErr(err)
		}
		log.Printf("[INFO] Unmounting /mnt/%s", d.Id())
		if err = mp.Delete(); err != nil {
			return diag.FromErr(err)
		}
		return nil
	}
}

// ValidateMountDirectory is a ValidateFunc that ensures the mount directory starts with a '/'
func ValidateMountDirectory(val interface{}, key string) (warns []string, errs []error) {
	v := val.(string)
	if v != "" && !strings.HasPrefix(v, "/") {
		return nil, []error{fmt.Errorf("%s must start with /, got: %s", key, v)}
	}
	return nil, nil
}
