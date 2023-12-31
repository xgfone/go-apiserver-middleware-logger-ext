// Copyright 2024 xgfone
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package loggerext

import "testing"

func TestContainsCT(t *testing.T) {
	_ = logBodyTypes.Set([]string{"text/*", "application/json", "*/xml"})

	if !containsct("text/plain") {
		t.Errorf("expect to contain '%s', but got not", "text/plain")
	}

	if !containsct("application/xml") {
		t.Errorf("expect to contain '%s', but got not", "application/xml")
	}

	if !containsct("application/json") {
		t.Errorf("expect to contain '%s', but got not", "application/json")
	}

	if containsct("application/x-www-form-urlencoded") {
		t.Errorf("unexpect to contain '%s'", "application/x-www-form-urlencoded")
	}
}
