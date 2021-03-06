package identity

import (
	"context"
	"fmt"

	"github.com/databrickslabs/terraform-provider-databricks/common"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// ResourceUserInstanceProfile binds user and instance profile
func ResourceUserInstanceProfile() *schema.Resource {
	return common.NewPairID("user_id", "instance_profile_id").Schema(func(
		m map[string]*schema.Schema) map[string]*schema.Schema {
		m["instance_profile_id"].ValidateDiagFunc = ValidInstanceProfile
		return m
	}).BindResource(common.BindResource{
		CreateContext: func(ctx context.Context, userID, roleARN string, c *common.DatabricksClient) error {
			return NewUsersAPI(ctx, c).Patch(userID, scimPatchRequest("add", "roles", roleARN))
		},
		ReadContext: func(ctx context.Context, userID, roleARN string, c *common.DatabricksClient) error {
			user, err := NewUsersAPI(ctx, c).read(userID)
			hasRole := complexValues(user.Roles).HasValue(roleARN)
			if err == nil && !hasRole {
				return common.NotFound("User has no role")
			}
			return err
		},
		DeleteContext: func(ctx context.Context, userID, roleARN string, c *common.DatabricksClient) error {
			return NewUsersAPI(ctx, c).Patch(userID, scimPatchRequest(
				"remove", fmt.Sprintf(`roles[value eq "%s"]`, roleARN), ""))
		},
	})
}
