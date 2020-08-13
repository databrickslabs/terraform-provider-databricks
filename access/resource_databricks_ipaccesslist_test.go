package access

// REST API: https://docs.databricks.com/dev-tools/api/latest/ip-access-list.html#operation/create-list

import (
	"net/http"
	"testing"

	"github.com/databrickslabs/databricks-terraform/common"
	"github.com/databrickslabs/databricks-terraform/internal/qa"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/stretchr/testify/assert"
)

var (
	TestingID               = "234567"
	TestingLabel            = "Naughty"
	TestingListTypeString   = "BLACKLIST"
	TestingListType         = IPAccessListType(TestingListTypeString)
	TestingEnabled          = true
	TestingIPAddresses      = []string{"1.2.3.4", "1.2.4.0/24"}
	TestingIPAddressesState = []interface{}{"1.2.3.4", "1.2.4.0/24"}
)

func TestIPACLCreate(t *testing.T) {
	d, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodPost,
			Resource: "/api/2.0/preview/ip-access-lists",
			Response: IPAccessListStatusWrapper{
				IPAccessList: IPAccessListStatus{
					ListID:        TestingID,
					Label:         TestingLabel,
					ListType:      TestingListType,
					IPAddresses:   TestingIPAddresses,
					AddressCount:  2,
					CreatedAt:     87939234,
					CreatorUserID: 1234556,
					UpdatedAt:     87939234,
					UpdatorUserID: 1234556,
					Enabled:       TestingEnabled,
				},
			},
		},
		{
			Method:   http.MethodGet,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID + "?",
			Response: IPAccessListStatusWrapper{
				IPAccessList: IPAccessListStatus{
					ListID:        TestingID,
					Label:         TestingLabel,
					ListType:      TestingListType,
					IPAddresses:   TestingIPAddresses,
					AddressCount:  2,
					CreatedAt:     87939234,
					CreatorUserID: 1234556,
					UpdatedAt:     87939234,
					UpdatorUserID: 1234556,
					Enabled:       TestingEnabled,
				},
			},
		},
	}, ResourceIPAccessList,
		map[string]interface{}{
			"label":        TestingLabel,
			"list_type":    TestingListTypeString,
			"ip_addresses": TestingIPAddressesState,
		},
		ResourceIPACLCreate,
	)
	assert.NoError(t, err, err)
	assert.Equal(t, TestingID, d.Id())
	assert.Equal(t, TestingLabel, d.Get("label"))
	assert.Equal(t, TestingListTypeString, d.Get("list_type"))
	assert.Equal(t, TestingEnabled, d.Get("enabled"))
	assert.Equal(t, 2, d.Get("ip_addresses.#"))
}

func TestAPIACLCreate_AlreadyExists(t *testing.T) {
	_, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodPost,
			Resource: "/api/2.0/preview/ip-access-lists",
			Response: APIErrorBody{
				ErrorCode: "RESOURCE_ALREADY_EXISTS",
				Message:   "IP access list with type (" + TestingListTypeString + ") and label (" + TestingLabel + ") already exists",
			},
			Status: 400,
		},
	},
		ResourceIPAccessList,
		map[string]interface{}{
			"label":        TestingLabel,
			"list_type":    TestingListTypeString,
			"ip_addresses": TestingIPAddressesState,
		},
		resourceIPACLCreate,
	)
	assert.Error(t, err)
}

func TestIPACLRead(t *testing.T) {
	d, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodGet,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID + "?",
			Response: IPAccessListStatusWrapper{
				IPAccessList: IPAccessListStatus{
					ListID:        TestingID,
					Label:         TestingLabel,
					ListType:      TestingListType,
					IPAddresses:   TestingIPAddresses,
					AddressCount:  2,
					CreatedAt:     87939234,
					CreatorUserID: 1234556,
					UpdatedAt:     87939234,
					UpdatorUserID: 1234556,
					Enabled:       TestingEnabled,
				},
			},
		},
	}, ResourceIPAccessList, nil, func(d *schema.ResourceData, c interface{}) error {
		d.SetId(TestingID)
		return resourceIPACLRead(d, c)
	})
	assert.NoError(t, err, err)
	assert.Equal(t, TestingID, d.Id())
	assert.Equal(t, TestingLabel, d.Get("label"))
	assert.Equal(t, TestingListTypeString, d.Get("list_type"))
	assert.Equal(t, TestingEnabled, d.Get("enabled"))
	assert.Equal(t, 2, d.Get("ip_addresses.#"))
}

func TestIPACLRead_404(t *testing.T) {
	d, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodGet,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID + "?",
			Response: common.APIErrorBody{
				ErrorCode: "RESOURCE_DOES_NOT_EXIST",
				Message:   "Can't find an IP access list with id: " + TestingID + ".",
			},
			Status: 404,
		},
	}, ResourceIPAccessList, nil, func(d *schema.ResourceData, c interface{}) error {
		d.SetId(TestingID)
		return resourceIPACLRead(d, c)
	})
	assert.NoError(t, err)
	assert.Equal(t, "", d.Id())
}

func TestIPACLRead_SERVERERROR(t *testing.T) {
	_, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodGet,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID + "?",
			Response: common.APIErrorBody{
				ErrorCode: "SERVER_ERROR",
				Message:   "Something unexpected happened",
			},
			Status: 500,
		},
	}, ResourceIPAccessList, nil, func(d *schema.ResourceData, c interface{}) error {
		d.SetId(TestingID)
		return resourceIPACLRead(d, c)
	})
	assert.Error(t, err)
}

func TestIPACLUpdate(t *testing.T) {
	d, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodGet,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID + "?",
			Response: IPAccessListStatusWrapper{
				IPAccessList: IPAccessListStatus{
					ListID:        TestingID,
					Label:         TestingLabel,
					ListType:      TestingListType,
					IPAddresses:   TestingIPAddresses,
					AddressCount:  2,
					CreatedAt:     87939234,
					CreatorUserID: 1234556,
					UpdatedAt:     87939234,
					UpdatorUserID: 1234556,
					Enabled:       TestingEnabled,
				},
			},
		},
		{
			Method:   http.MethodPut,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID,
			Response: IPAccessListStatusWrapper{
				IPAccessList: IPAccessListStatus{
					ListID:        TestingID,
					Label:         TestingLabel,
					ListType:      TestingListType,
					IPAddresses:   TestingIPAddresses,
					AddressCount:  2,
					CreatedAt:     87939234,
					CreatorUserID: 1234556,
					UpdatedAt:     87939234,
					UpdatorUserID: 1234556,
					Enabled:       TestingEnabled,
				},
			},
		},
	},
		ResourceIPAccessList,
		map[string]interface{}{
			"label":        TestingLabel,
			"list_type":    TestingListTypeString,
			"ip_addresses": TestingIPAddressesState,
		},
		func(d *schema.ResourceData, c interface{}) error {
			d.SetId(TestingID)
			return resourceIPACLUpdate(d, c)
		})
	assert.NoError(t, err, err)
	assert.Equal(t, TestingID, d.Id())
	assert.Equal(t, TestingLabel, d.Get("label"))
	assert.Equal(t, TestingListTypeString, d.Get("list_type"))
	assert.Equal(t, TestingEnabled, d.Get("enabled"))
	assert.Equal(t, 2, d.Get("ip_addresses.#"))
}

func TestIPACLUpdate_ERROR(t *testing.T) {
	_, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodPut,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID,
			Response: common.APIErrorBody{
				ErrorCode: "SERVER_ERROR",
				Message:   "Something unexpected happened",
			},
			Status: 500,
		},
	}, ResourceIPAccessList, nil, func(d *schema.ResourceData, c interface{}) error {
		d.SetId(TestingID)
		return resourceIPACLUpdate(d, c)
	})
	assert.Error(t, err)
}

func TestIPACLDelete(t *testing.T) {
	_, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodDelete,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID,
		},
	}, ResourceIPAccessList, nil, func(d *schema.ResourceData, c interface{}) error {
		d.SetId(TestingID)
		return resourceIPACLDelete(d, c)
	})
	assert.NoError(t, err, err)
}

func TestIPACLDelete_NONEXISTANT(t *testing.T) {
	_, err := qa.ResourceFixture(t, []qa.HTTPFixture{
		{
			Method:   http.MethodDelete,
			Resource: "/api/2.0/preview/ip-access-lists/" + TestingID,
			Response: common.APIErrorBody{
				ErrorCode: "FEATURE_DISABLE",
				Message:   "IP access list is not available in the pricing tier of this workspace",
			},
			Status: 404,
		},
	}, ResourceIPAccessList, nil, func(d *schema.ResourceData, c interface{}) error {
		d.SetId(TestingID)
		return resourceIPACLDelete(d, c)
	})
	assert.Error(t, err)
}
