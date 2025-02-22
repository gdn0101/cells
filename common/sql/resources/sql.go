/*
 * Copyright (c) 2018. Abstrium SAS <team (at) pydio.com>
 * This file is part of Pydio Cells.
 *
 * Pydio Cells is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * Pydio Cells is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with Pydio Cells.  If not, see <http://www.gnu.org/licenses/>.
 *
 * The latest code can be found at <https://pydio.com>.
 */

package resources

import (
	"context"
	"fmt"
	"github.com/patrickmn/go-cache"
	"time"

	"github.com/pydio/packr"
	migrate "github.com/rubenv/sql-migrate"

	service "github.com/pydio/cells/common/service/proto"
	"github.com/pydio/cells/common/sql"
	"github.com/pydio/cells/x/configx"
	"gopkg.in/doug-martin/goqu.v4"
)

var (
	queries = map[string]string{
		"AddRuleForResource":              "insert into %%PREFIX%%_policies (resource, action, subject, effect, conditions) values (?, ?, ?, ?, ?)",
		"SelectRulesForResource":          "select * from %%PREFIX%%_policies where resource=?",
		"DeleteRulesForResource":          "delete from %%PREFIX%%_policies where resource=?",
		"DeleteRulesForResourceAndAction": "delete from %%PREFIX%%_policies where resource=? and action=?",
		"DeleteRulesForSubject":           "delete from %%PREFIX%%_policies where subject=?",
	}
)

// Implementation of the SQL interface.
type ResourcesSQL struct {
	*sql.Handler

	LeftIdentifier string
	cache          *cache.Cache
}

// Init performs necessary up migration.
func (s *ResourcesSQL) Init(options configx.Values) error {

	s.cache = cache.New(30*time.Second, 2*time.Minute)

	migrations := &sql.PackrMigrationSource{
		Box:         packr.NewBox("../../../common/sql/resources/migrations"),
		Dir:         "./" + s.Driver(),
		TablePrefix: s.Prefix() + "_policies",
	}
	_, err := sql.ExecMigration(s.DB(), s.Driver(), migrations, migrate.Up, s.Prefix()+"_policies")
	if err != nil {
		return err
	}

	if options.Val("prepare").Default(true).Bool() {
		for key, query := range queries {
			if err := s.Prepare(key, query); err != nil {
				fmt.Println(err)
				return err
			}
		}
	}

	return nil
}

// AddPolicy persists a policy in the underlying storage
func (s *ResourcesSQL) AddPolicy(resourceId string, policy *service.ResourcePolicy) error {

	s.cache.Delete(resourceId)

	prepared, er := s.GetStmt("AddRuleForResource")
	if er != nil {
		return er
	}

	_, err := prepared.Exec(resourceId, policy.Action.String(), policy.Subject, policy.Effect.String(), policy.JsonConditions)
	return err

}

// AddPolicies persists a set of policies. If update is true, it replace them by deleting existing ones
func (s *ResourcesSQL) AddPolicies(update bool, resourceId string, policies []*service.ResourcePolicy) error {

	s.cache.Delete(resourceId)

	tx, errTx := s.DB().BeginTx(context.Background(), nil)
	if errTx != nil {
		return errTx
	}

	// Checking transaction went fine
	defer func() {
		if errTx != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	if update {
		if deleteRules, er := s.GetStmt("DeleteRulesForResource"); er != nil {
			errTx = er
			return errTx
		} else if txStmt := tx.Stmt(deleteRules.GetSQLStmt()); txStmt != nil {
			defer txStmt.Close()
			if _, errTx = txStmt.Exec(resourceId); errTx != nil {
				return errTx
			}
		} else {
			return fmt.Errorf("empty statement")
		}
	}
	//fmt.Println("ADDING ", len(policies), "POLICIES ON RESOURCE ", resourceId, " WITH UPDATE ? ", update)

	if addRule, er := s.GetStmt("AddRuleForResource"); er != nil {
		errTx = er
		return errTx
	} else if txStmt := tx.Stmt(addRule.GetSQLStmt()); txStmt != nil {
		defer txStmt.Close()
		for _, policy := range policies {
			if _, errTx = txStmt.Exec(resourceId, policy.Action.String(), policy.Subject, policy.Effect.String(), policy.JsonConditions); errTx != nil {
				return errTx
			}
		}
	} else {
		return fmt.Errorf("empty statement")
	}

	return nil
}

// GetPoliciesForResource finds all policies for a given resource
func (s *ResourcesSQL) GetPoliciesForResource(resourceId string) ([]*service.ResourcePolicy, error) {

	if cached, ok := s.cache.Get(resourceId); ok {
		if rules, o := cached.([]*service.ResourcePolicy); o {
			return rules, nil
		}
	}

	var res []*service.ResourcePolicy

	prepared, er := s.GetStmt("SelectRulesForResource")
	if er != nil {
		return nil, er
	}

	timeout, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	rows, err := prepared.QueryContext(timeout, resourceId)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	for rows.Next() {
		rule := new(service.ResourcePolicy)
		var actionString string
		var effectString string
		if e := rows.Scan(&rule.Id, &rule.Resource, &actionString, &rule.Subject, &effectString, &rule.JsonConditions); e != nil {
			return res, e
		}
		rule.Action = service.ResourcePolicyAction(service.ResourcePolicyAction_value[actionString])
		rule.Effect = service.ResourcePolicy_PolicyEffect(service.ResourcePolicy_PolicyEffect_value[effectString])
		res = append(res, rule)
	}

	s.cache.Set(resourceId, res, cache.DefaultExpiration)

	return res, nil
}

// DeletePoliciesForResource removes all policies for a given resource
func (s *ResourcesSQL) DeletePoliciesForResource(resourceId string) error {

	s.cache.Delete(resourceId)

	prepared, er := s.GetStmt("DeleteRulesForResource")
	if er != nil {
		return er
	}

	_, err := prepared.Exec(resourceId)
	return err

}

// DeletePoliciesForResource removes all policies for a given resource
func (s *ResourcesSQL) DeletePoliciesBySubject(subject string) error {

	// Delete cache items that would contain this subject
	for k, i := range s.cache.Items() {
		if rules, ok := i.Object.([]*service.ResourcePolicy); ok {
			for _, pol := range rules {
				if pol.Subject == subject {
					s.cache.Delete(k)
					break
				}
			}
		}
	}

	prepared, er := s.GetStmt("DeleteRulesForSubject")
	if er != nil {
		return er
	}

	_, err := prepared.Exec(subject)
	return err

}

// DeletePoliciesForResourceAndAction removes policies for a given resource only if they have the corresponding action
func (s *ResourcesSQL) DeletePoliciesForResourceAndAction(resourceId string, action service.ResourcePolicyAction) error {

	s.cache.Delete(resourceId)

	prepared, er := s.GetStmt("DeleteRulesForResourceAndAction")
	if er != nil {
		return er
	}

	_, err := prepared.Exec(resourceId, action.String())
	return err

}

// BuildPolicyConditionForAction builds an ResourcesSQL condition from claims toward the associated resource table
func (s *ResourcesSQL) BuildPolicyConditionForAction(q *service.ResourcePolicyQuery, action service.ResourcePolicyAction) (expr goqu.Expression, e error) {

	if q == nil || q.Any {
		return nil, nil
	}

	leftIdentifier := s.LeftIdentifier
	resourcesTableName := s.Prefix() + "_policies"
	subjects := q.GetSubjects()

	if q.Empty {
		join := goqu.I(resourcesTableName + ".resource").Eq(goqu.I(leftIdentifier))
		actionQ := goqu.I(resourcesTableName + ".action").Eq(action.String())
		str, args, e := goqu.New(s.Driver(), s.DB()).
			From(resourcesTableName).
			Prepared(true).
			Select(goqu.L("1")).
			Where(goqu.And(join, actionQ)).
			ToSql()

		if e != nil {
			return nil, e
		}

		return goqu.L("NOT EXISTS ("+str+")", args...), nil

	} else {

		resSubject := resourcesTableName + ".subject"
		var ors []goqu.Expression
		var ands []goqu.Expression
		if len(subjects) > 0 {
			for _, subject := range subjects {
				ors = append(ors, goqu.I(resSubject).Eq(subject))
			}
			ands = append(ands, goqu.Or(ors...))
		}

		ands = append(ands, goqu.I(resourcesTableName+".resource").Eq(goqu.I(leftIdentifier))) // Join
		ands = append(ands, goqu.I(resourcesTableName+".action").Eq(action.String()))
		str, args, e := goqu.New(s.Driver(), s.DB()).
			From(resourcesTableName).
			Prepared(true).
			Select(goqu.L("1")).
			Where(goqu.And(ands...)).
			ToSql()

		if e != nil {
			return nil, e
		}
		return goqu.L("EXISTS ("+str+")", args...), nil

	}
}
