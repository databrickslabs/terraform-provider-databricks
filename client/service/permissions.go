package service

import (
	"encoding/json"
	"net/http"

	"github.com/databrickslabs/databricks-terraform/client/model"
)

// PermissionsAPI exposes general permission related methods
type PermissionsAPI struct {
	client *DatabricksClient
}

// AddOrModify works with permissions change list
func (a PermissionsAPI) AddOrModify(objectID string, objectACL *model.AccessControlChangeList) error {
	_, err := a.client.performQuery(http.MethodPatch,
		"/preview/permissions"+objectID,
		"2.0", nil, objectACL)
	if err != nil {
		return err
	}

	return err
}

// SetOrDelete updates object permissions
func (a PermissionsAPI) SetOrDelete(objectID string, objectACL *model.AccessControlChangeList) error {
	_, err := a.client.performQuery(http.MethodPut,
		"/preview/permissions"+objectID,
		"2.0", nil, objectACL)
	if err != nil {
		return err
	}

	return err
}

// Read gets all relevant permissions for the object, including inherited ones
func (a PermissionsAPI) Read(objectID string) (*model.ObjectACL, error) {
	resp, err := a.client.performQuery(http.MethodGet,
		"/preview/permissions"+objectID,
		"2.0", nil, nil)
	if err != nil {
		return nil, err
	}
	var objectACL = new(model.ObjectACL)
	err = json.Unmarshal(resp, &objectACL)
	return objectACL, err
}
