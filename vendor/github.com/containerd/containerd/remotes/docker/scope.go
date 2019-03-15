/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package docker

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/containerd/containerd/reference"
)

type tokenScopes map[string]tokenScope

func (scopes tokenScopes) add(ts tokenScope) {
	match, ok := scopes[ts.resource]
	if !ok {
		scopes[ts.resource] = ts
		return
	}
	for k := range ts.actions {
		match.actions[k] = nil
	}
	scopes[ts.resource] = match
}

func (scopes tokenScopes) merge(other tokenScopes) {
	for _, v := range other {
		scopes.add(v)
	}
}

func (scopes tokenScopes) contains(other tokenScopes) bool {
	if other == nil {
		return true
	}
	if scopes == nil {
		return false
	}
	for k, v := range other {
		existing, exists := scopes[k]
		if !exists {
			return false
		}
		for action := range v.actions {
			if _, ok := existing.actions[action]; !ok {
				return false
			}
		}
	}
	return true
}

func (scopes tokenScopes) flatten() []string {
	var result []string
	for _, s := range scopes {
		result = append(result, s.String())
	}
	sort.Strings(result)
	return result
}

func (scopes tokenScopes) clone() tokenScopes {
	if scopes == nil {
		return nil
	}
	result := tokenScopes{}
	for k, v := range scopes {
		result[k] = v.clone()
	}
	return result
}

// repositoryScope returns a repository scope string such as "repository:foo/bar:pull"
// for "host/foo/bar:baz".
// When push is true, both pull and push are added to the scope.
func repositoryScope(refspec reference.Spec, push bool) (tokenScope, error) {
	u, err := url.Parse("dummy://" + refspec.Locator)
	if err != nil {
		return tokenScope{}, err
	}
	ts := tokenScope{
		resource: "repository:" + strings.TrimPrefix(u.Path, "/"),
		actions: map[string]interface{}{
			"pull": struct{}{},
		},
	}
	if push {
		ts.actions["push"] = struct{}{}
	}
	return ts, nil
}

// tokenScopesKey is used for the key for context.WithValue().
// value: []string (e.g. {"registry:foo/bar:pull"})
type tokenScopesKey struct{}

// contextWithRepositoryScope returns a context with tokenScopesKey{} and the repository scope value.
func contextWithRepositoryScope(ctx context.Context, refspec reference.Spec, push bool) (context.Context, error) {
	s, err := repositoryScope(refspec, push)
	if err != nil {
		return nil, err
	}
	scopes := getContextScopes(ctx).clone()
	if scopes == nil {
		scopes = tokenScopes{}
	}
	scopes.add(s)

	return context.WithValue(ctx, tokenScopesKey{}, scopes), nil
}

func getContextScopes(ctx context.Context) tokenScopes {
	var existingTokens tokenScopes
	if rawExiting := ctx.Value(tokenScopesKey{}); rawExiting != nil {
		existingTokens, _ = rawExiting.(tokenScopes)
	}
	return existingTokens
}

type tokenScope struct {
	resource string
	actions  map[string]interface{}
}

func (ts tokenScope) String() string {
	var actionSlice []string
	for k := range ts.actions {
		actionSlice = append(actionSlice, k)
	}
	sort.Strings(actionSlice)
	return fmt.Sprintf("%s:%s", ts.resource, strings.Join(actionSlice, ","))
}

func (ts tokenScope) clone() tokenScope {
	result := tokenScope{resource: ts.resource}
	if ts.actions == nil {
		return result
	}
	result.actions = map[string]interface{}{}
	for k, v := range ts.actions {
		result.actions[k] = v
	}
	return result
}

func parseTokenScope(s string) (tokenScope, error) {
	lastSep := strings.LastIndex(s, ":")
	if lastSep == -1 {
		return tokenScope{}, fmt.Errorf("%q is not a valid token scope", s)
	}
	actions := make(map[string]interface{})
	for _, a := range strings.Split(s[lastSep+1:], ",") {
		actions[a] = nil
	}
	return tokenScope{
		resource: s[:lastSep],
		actions:  actions,
	}, nil
}

// getTokenScopes returns a map of resource -> tokenScope from ctx.Value(tokenScopesKey{}) and params["scope"].
func getTokenScopes(ctx context.Context, params map[string]string) (tokenScopes, error) {
	tokenScopes := tokenScopes{}
	if params != nil {
		if paramScopesFlat, ok := params["scope"]; ok {
			paramScopes := strings.Split(paramScopesFlat, " ")
			for _, rawScope := range paramScopes {
				parsedScope, err := parseTokenScope(rawScope)
				if err != nil {
					return nil, err
				}
				tokenScopes.add(parsedScope)
			}
		}
	}
	if contextScopes := getContextScopes(ctx); contextScopes != nil {
		tokenScopes.merge(contextScopes)
	}
	return tokenScopes, nil
}
