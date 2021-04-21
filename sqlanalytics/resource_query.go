package sqlanalytics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"

	"github.com/databrickslabs/terraform-provider-databricks/common"
	"github.com/databrickslabs/terraform-provider-databricks/sqlanalytics/api"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// QueryEntity defines the parameters that can be set in the resource.
type QueryEntity struct {
	DataSourceID string           `json:"data_source_id"`
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	Query        string           `json:"query"`
	Schedule     *QuerySchedule   `json:"schedule,omitempty"`
	Tags         []string         `json:"tags,omitempty"`
	Parameter    []QueryParameter `json:"parameter,omitempty"`
}

// QuerySchedule ...
type QuerySchedule struct {
	Continuous *QueryScheduleContinuous `json:"continuous,omitempty"`
	Daily      *QueryScheduleDaily      `json:"daily,omitempty"`
	Weekly     *QueryScheduleWeekly     `json:"weekly,omitempty"`
}

// QueryScheduleContinuous ...
type QueryScheduleContinuous struct {
	IntervalSeconds int    `json:"interval_seconds"`
	UntilDate       string `json:"until_date,omitempty"`
}

// QueryScheduleDaily ...
type QueryScheduleDaily struct {
	IntervalDays int    `json:"interval_days"`
	TimeOfDay    string `json:"time_of_day"`
	UntilDate    string `json:"until_date,omitempty"`
}

// QueryScheduleWeekly ...
type QueryScheduleWeekly struct {
	IntervalWeeks int    `json:"interval_weeks"`
	DayOfWeek     string `json:"day_of_week"`
	TimeOfDay     string `json:"time_of_day"`
	UntilDate     string `json:"until_date,omitempty"`
}

// QueryParameter ...
type QueryParameter struct {
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`

	// Type specific structs.
	// Only one of them may be set.
	Text             *QueryParameterText          `json:"text,omitempty"`
	Number           *QueryParameterNumber        `json:"number,omitempty"`
	Enum             *QueryParameterEnum          `json:"enum,omitempty"`
	Query            *QueryParameterQuery         `json:"query,omitempty"`
	Date             *QueryParameterDateLike      `json:"date,omitempty"`
	DateTime         *QueryParameterDateLike      `json:"datetime,omitempty"`
	DateTimeSec      *QueryParameterDateLike      `json:"datetimesec,omitempty"`
	DateRange        *QueryParameterDateRangeLike `json:"date_range,omitempty"`
	DateTimeRange    *QueryParameterDateRangeLike `json:"datetime_range,omitempty"`
	DateTimeSecRange *QueryParameterDateRangeLike `json:"datetimesec_range,omitempty"`
}

// QueryParameterText ...
type QueryParameterText struct {
	Value string `json:"value"`
}

// QueryParameterNumber ...
type QueryParameterNumber struct {
	Value float64 `json:"value"`
}

// QueryParameterEnum ...
type QueryParameterEnum struct {
	// Value iff `multiple == nil`
	Value string `json:"value,omitempty"`
	// Values iff `multiple != nil`
	Values []string `json:"values,omitempty"`

	Options  []string                     `json:"options"`
	Multiple *QueryParameterAllowMultiple `json:"multiple,omitempty"`
}

// QueryParameterQuery ...
type QueryParameterQuery struct {
	// Value iff `multiple == nil`
	Value string `json:"value,omitempty"`
	// Values iff `multiple != nil`
	Values []string `json:"values,omitempty"`

	QueryID  string                       `json:"query_id"`
	Multiple *QueryParameterAllowMultiple `json:"multiple,omitempty"`
}

// QueryParameterDateLike ...
type QueryParameterDateLike struct {
	Value string `json:"value"`
}

// QueryParameterDateRangeLike ...
type QueryParameterDateRangeLike struct {
	Value string `json:"value"`
}

// QueryParameterAllowMultiple ...
type QueryParameterAllowMultiple struct {
	Prefix    string `json:"prefix"`
	Suffix    string `json:"suffix"`
	Separator string `json:"separator"`
}

func (q *QueryParameterAllowMultiple) toAPIObject() *api.QueryParameterMultipleValuesOptions {
	return &api.QueryParameterMultipleValuesOptions{
		Prefix:    q.Prefix,
		Suffix:    q.Suffix,
		Separator: q.Separator,
	}
}

func newQueryParameterAllowMultiple(aq *api.QueryParameterMultipleValuesOptions) *QueryParameterAllowMultiple {
	if aq == nil {
		return nil
	}
	return &QueryParameterAllowMultiple{
		Prefix:    aq.Prefix,
		Suffix:    aq.Suffix,
		Separator: aq.Separator,
	}
}

const secondsInDay = 24 * 60 * 60
const secondsInWeek = 7 * secondsInDay

func (q *QueryEntity) toAPIObject(schema map[string]*schema.Schema, data *schema.ResourceData) (*api.Query, error) {
	// Extract from ResourceData.
	if err := common.DataToStructPointer(data, schema, q); err != nil {
		return nil, err
	}

	// Transform to API object.
	var aq api.Query
	aq.ID = data.Id()
	aq.DataSourceID = q.DataSourceID
	aq.Name = q.Name
	aq.Description = q.Description
	aq.Query = q.Query
	aq.Tags = append([]string{}, q.Tags...)

	if s := q.Schedule; s != nil {
		if sp := s.Continuous; sp != nil {
			aq.Schedule = &api.QuerySchedule{
				Interval: sp.IntervalSeconds,
			}
			if sp.UntilDate != "" {
				aq.Schedule.Until = &sp.UntilDate
			}
		}
		if sp := s.Daily; sp != nil {
			aq.Schedule = &api.QuerySchedule{
				Interval: sp.IntervalDays * secondsInDay,
				Time:     &sp.TimeOfDay,
			}
			if sp.UntilDate != "" {
				aq.Schedule.Until = &sp.UntilDate
			}
		}
		if sp := s.Weekly; sp != nil {
			aq.Schedule = &api.QuerySchedule{
				Interval:  sp.IntervalWeeks * secondsInWeek,
				DayOfWeek: &sp.DayOfWeek,
				Time:      &sp.TimeOfDay,
			}
			if sp.UntilDate != "" {
				aq.Schedule.Until = &sp.UntilDate
			}
		}
	}

	if len(q.Parameter) > 0 {
		aq.Options = &api.QueryOptions{}
		for _, p := range q.Parameter {
			ap := api.QueryParameter{
				Name:  p.Name,
				Title: p.Title,
			}

			var iface interface{}

			switch {
			case p.Text != nil:
				iface = api.QueryParameterText{
					QueryParameter: ap,
					Value:          p.Text.Value,
				}
			case p.Number != nil:
				iface = api.QueryParameterNumber{
					QueryParameter: ap,
					Value:          p.Number.Value,
				}
			case p.Enum != nil:
				tmp := api.QueryParameterEnum{
					QueryParameter: ap,
					Options:        strings.Join(p.Enum.Options, "\n"),
				}
				if p.Enum.Multiple != nil {
					tmp.Values = p.Enum.Values
					tmp.Multi = p.Enum.Multiple.toAPIObject()
				} else {
					tmp.Values = []string{p.Enum.Value}
				}
				iface = tmp
			case p.Query != nil:
				tmp := api.QueryParameterQuery{
					QueryParameter: ap,
					QueryID:        p.Query.QueryID,
				}
				if p.Query.Multiple != nil {
					tmp.Values = p.Query.Values
					tmp.Multi = p.Query.Multiple.toAPIObject()
				} else {
					tmp.Values = []string{p.Query.Value}
				}
				iface = tmp
			case p.Date != nil:
				iface = api.QueryParameterDate{
					QueryParameter: ap,
					Value:          p.Date.Value,
				}
			case p.DateTime != nil:
				iface = api.QueryParameterDateTime{
					QueryParameter: ap,
					Value:          p.DateTime.Value,
				}
			case p.DateTimeSec != nil:
				iface = api.QueryParameterDateTimeSec{
					QueryParameter: ap,
					Value:          p.DateTimeSec.Value,
				}
			case p.DateRange != nil:
				iface = api.QueryParameterDateRange{
					QueryParameter: ap,
					Value:          p.DateRange.Value,
				}
			case p.DateTimeRange != nil:
				iface = api.QueryParameterDateTimeRange{
					QueryParameter: ap,
					Value:          p.DateTimeRange.Value,
				}
			case p.DateTimeSecRange != nil:
				iface = api.QueryParameterDateTimeSecRange{
					QueryParameter: ap,
					Value:          p.DateTimeSecRange.Value,
				}
			default:
				log.Fatalf("Don't know what to do for QueryParameter...")
			}

			aq.Options.Parameters = append(aq.Options.Parameters, iface)
		}
	}

	return &aq, nil
}

func (q *QueryEntity) fromAPIObject(aq *api.Query, schema map[string]*schema.Schema, data *schema.ResourceData) error {
	// Copy from API object.
	q.DataSourceID = aq.DataSourceID
	q.Name = aq.Name
	q.Description = aq.Description
	q.Query = aq.Query
	q.Tags = append([]string{}, aq.Tags...)

	if s := aq.Schedule; s != nil {
		q.Schedule = &QuerySchedule{}
		switch {
		case s.Interval%secondsInWeek == 0:
			q.Schedule.Weekly = &QueryScheduleWeekly{
				IntervalWeeks: s.Interval / secondsInWeek,
			}
			if s.DayOfWeek != nil {
				q.Schedule.Weekly.DayOfWeek = *s.DayOfWeek
			}
			if s.Time != nil {
				q.Schedule.Weekly.TimeOfDay = *s.Time
			}
			if s.Until != nil {
				q.Schedule.Weekly.UntilDate = *s.Until
			}
		case s.Interval%secondsInDay == 0:
			q.Schedule.Daily = &QueryScheduleDaily{
				IntervalDays: s.Interval / secondsInDay,
			}
			if s.Time != nil {
				q.Schedule.Daily.TimeOfDay = *s.Time
			}
			if s.Until != nil {
				q.Schedule.Daily.UntilDate = *s.Until
			}
		default:
			q.Schedule.Continuous = &QueryScheduleContinuous{
				IntervalSeconds: s.Interval,
			}
			if s.Until != nil {
				q.Schedule.Continuous.UntilDate = *s.Until
			}
		}
	} else {
		// Overwrite `schedule` in case it's not set on the server side.
		// This would have been skipped by `common.StructToData` because of slice emptiness.
		// Ideally, the reflection code also sets empty values, but we'd risk
		// clobbering values we actually want to keep around in existing code.
		data.Set("schedule", nil)
	}

	if aq.Options != nil {
		q.Parameter = nil

		for _, ap := range aq.Options.Parameters {
			var p QueryParameter
			switch apv := ap.(type) {
			case *api.QueryParameterText:
				p.Name = apv.Name
				p.Title = apv.Title
				p.Text = &QueryParameterText{
					Value: apv.Value,
				}
			case *api.QueryParameterNumber:
				p.Name = apv.Name
				p.Title = apv.Title
				p.Number = &QueryParameterNumber{
					Value: apv.Value,
				}
			case *api.QueryParameterEnum:
				p.Name = apv.Name
				p.Title = apv.Title
				p.Enum = &QueryParameterEnum{
					Options:  strings.Split(apv.Options, "\n"),
					Multiple: newQueryParameterAllowMultiple(apv.Multi),
				}
				if p.Enum.Multiple != nil {
					p.Enum.Values = apv.Values
				} else {
					p.Enum.Value = apv.Values[0]
				}
			case *api.QueryParameterQuery:
				p.Name = apv.Name
				p.Title = apv.Title
				p.Query = &QueryParameterQuery{
					QueryID:  apv.QueryID,
					Multiple: newQueryParameterAllowMultiple(apv.Multi),
				}
				if p.Query.Multiple != nil {
					p.Query.Values = apv.Values
				} else {
					p.Query.Value = apv.Values[0]
				}
			case *api.QueryParameterDate:
				p.Name = apv.Name
				p.Title = apv.Title
				p.Date = &QueryParameterDateLike{
					Value: apv.Value,
				}
			case *api.QueryParameterDateTime:
				p.Name = apv.Name
				p.Title = apv.Title
				p.DateTime = &QueryParameterDateLike{
					Value: apv.Value,
				}
			case *api.QueryParameterDateTimeSec:
				p.Name = apv.Name
				p.Title = apv.Title
				p.DateTimeSec = &QueryParameterDateLike{
					Value: apv.Value,
				}
			case *api.QueryParameterDateRange:
				p.Name = apv.Name
				p.Title = apv.Title
				p.DateRange = &QueryParameterDateRangeLike{
					Value: apv.Value,
				}
			case *api.QueryParameterDateTimeRange:
				p.Name = apv.Name
				p.Title = apv.Title
				p.DateTimeRange = &QueryParameterDateRangeLike{
					Value: apv.Value,
				}
			case *api.QueryParameterDateTimeSecRange:
				p.Name = apv.Name
				p.Title = apv.Title
				p.DateTimeSecRange = &QueryParameterDateRangeLike{
					Value: apv.Value,
				}
			default:
				log.Fatalf("Don't know what to do for type: %#v", reflect.TypeOf(apv).String())
			}

			q.Parameter = append(q.Parameter, p)
		}
	}

	// Transform to ResourceData.
	return common.StructToData(*q, schema, data)
}

// NewQueryAPI ...
func NewQueryAPI(ctx context.Context, m interface{}) QueryAPI {
	return QueryAPI{m.(*common.DatabricksClient), ctx}
}

// QueryAPI ...
type QueryAPI struct {
	client  *common.DatabricksClient
	context context.Context
}

func (a QueryAPI) buildPath(path ...string) string {
	out := "/preview/sql/queries"
	if len(path) == 1 {
		out = out + "/" + strings.Join(path, "/")
	}
	return out
}

// Create ...
func (a QueryAPI) Create(q *api.Query) (*api.Query, error) {
	var qp api.Query
	err := a.client.Post(a.context, a.buildPath(), q, &qp)
	if err != nil {
		return nil, err
	}

	// New queries are created with a table visualization by default.
	// We don't manage that visualization here, so immediately remove it.
	if len(qp.Visualizations) > 0 {
		for _, rv := range qp.Visualizations {
			var v api.Visualization
			err = json.Unmarshal(rv, &v)
			if err != nil {
				return nil, err
			}
			// TODO
			// err = a.DeleteVisualization(&v)
			// if err != nil {
			// 	return nil, err
			// }
		}
		qp.Visualizations = []json.RawMessage{}
	}

	return &qp, err
}

// Read ...
func (a QueryAPI) Read(q *api.Query) (*api.Query, error) {
	var qp api.Query
	err := a.client.Get(a.context, a.buildPath(q.ID), nil, &qp)
	if err != nil {
		return nil, err
	}

	return &qp, nil
}

// Update ...
func (a QueryAPI) Update(q *api.Query) (*api.Query, error) {
	var qp api.Query
	err := a.client.Post(a.context, a.buildPath(q.ID), q, &qp)
	if err != nil {
		return nil, err
	}

	return &qp, nil
}

// Delete ...
func (a QueryAPI) Delete(q *api.Query) error {
	return a.client.Delete(a.context, a.buildPath(q.ID), nil)
}

// ResourceQuery ...
func ResourceQuery() *schema.Resource {
	s := common.StructToSchema(
		QueryEntity{},
		func(m map[string]*schema.Schema) map[string]*schema.Schema {
			schedule := m["schedule"].Elem.(*schema.Resource)

			// Make different query schedule types mutually exclusive.
			{
				ns := []string{"continuous", "daily", "weekly"}
				for _, n1 := range ns {
					for _, n2 := range ns {
						if n1 == n2 {
							continue
						}
						schedule.Schema[n1].ConflictsWith = append(schedule.Schema[n1].ConflictsWith, fmt.Sprintf("schedule.0.%s", n2))
					}
				}
			}

			// Validate week of day in weekly schedule.
			// Manually verified that this is case sensitive.
			weekly := schedule.Schema["weekly"].Elem.(*schema.Resource)
			weekly.Schema["day_of_week"].ValidateFunc = validation.StringInSlice([]string{
				"Sunday",
				"Monday",
				"Tuesday",
				"Wednesday",
				"Thursday",
				"Friday",
				"Saturday",
			}, false)
			return m
		})

	return common.Resource{
		Create: func(ctx context.Context, data *schema.ResourceData, c *common.DatabricksClient) error {
			var q QueryEntity
			aq, err := q.toAPIObject(s, data)
			if err != nil {
				return err
			}

			aqNew, err := NewQueryAPI(ctx, c).Create(aq)
			if err != nil {
				return err
			}

			// No need to set anything because the resource is going to be
			// read immediately after being created.
			data.SetId(aqNew.ID)
			return nil
		},
		Read: func(ctx context.Context, data *schema.ResourceData, c *common.DatabricksClient) error {
			var q QueryEntity
			aq, err := q.toAPIObject(s, data)
			if err != nil {
				return err
			}

			aqNew, err := NewQueryAPI(ctx, c).Read(aq)
			if err != nil {
				return err
			}

			return q.fromAPIObject(aqNew, s, data)
		},
		Update: func(ctx context.Context, data *schema.ResourceData, c *common.DatabricksClient) error {
			var q QueryEntity
			aq, err := q.toAPIObject(s, data)
			if err != nil {
				return err
			}

			_, err = NewQueryAPI(ctx, c).Update(aq)
			if err != nil {
				return err
			}

			// No need to set anything because the resource is going to be
			// read immediately after being created.
			return nil
		},
		Delete: func(ctx context.Context, data *schema.ResourceData, c *common.DatabricksClient) error {
			var q QueryEntity
			aq, err := q.toAPIObject(s, data)
			if err != nil {
				return err
			}

			return NewQueryAPI(ctx, c).Delete(aq)
		},
		Schema: s,
	}.ToResource()
}