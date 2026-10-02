package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/intercept"
)

type savedSetting struct {
	value string
	err   error
}

func (s savedSetting) GetSetting(context.Context, string) (string, error) { return s.value, s.err }

func TestRestoreInterceptConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		err         error
		invalid     bool
	}{
		{name: "absent", err: sql.ErrNoRows},
		{name: "legacy", value: `{"enabled":true,"rules":[{"enabled":true}]}`},
		{name: "full", value: `{"enabled":true,"rules":[],"responseEnabled":true,"responseRules":[{"enabled":true,"statusCode":201}],"replacementRules":[{"id":"a","direction":"response","target":"body","pattern":"a","replacement":"b"}]}`},
		{name: "malformed", value: `{"enabled":true,`, invalid: true},
		{name: "invalid", value: `{"enabled":true,"replacementRules":[{"id":"a","direction":"response","target":"body","pattern":"(","regex":true}]}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := intercept.NewController(nil, false, []intercept.Rule{{Enabled: true}})
			before := c.State()
			err := restoreInterceptConfig(context.Background(), savedSetting{tc.value, tc.err}, c)
			if (err != nil) != tc.invalid {
				t.Fatalf("restore error = %v", err)
			}
			if tc.invalid || tc.err != nil {
				if !reflect.DeepEqual(before, c.State()) {
					t.Fatal("changed default state")
				}
				return
			}
			want := before
			if err := json.Unmarshal([]byte(tc.value), &want); err != nil {
				t.Fatal(err)
			}
			// Controller clones empty lists as nil.
			gotJSON, _ := json.Marshal(c.State())
			wantController := intercept.NewController(nil, false, nil)
			wantController.Update(want)
			wantJSON, _ := json.Marshal(wantController.State())
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("got %s want %s", gotJSON, wantJSON)
			}
			if tc.name == "legacy" && (c.State().ResponseEnabled || len(c.State().ResponseRules) != 1 || !c.State().ResponseRules[0].Enabled) {
				t.Fatal("lost legacy defaults")
			}
		})
	}
}
