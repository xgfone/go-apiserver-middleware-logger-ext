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

import (
	"net/http"
	"net/url"
	"testing"
)

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

func TestAppendIgnorePath(t *testing.T) {
	AppendIgnorePath("")
	AppendIgnorePath("/")
	AppendIgnorePath("/path1")
	AppendIgnorePath("/path2/")

	req := &http.Request{URL: &url.URL{Path: "/"}}
	if Enabled(req) {
		t.Error("expect false, but got true")
	}

	req.URL.Path = "/path1"
	if Enabled(req) {
		t.Error("expect false, but got true")
	}

	req.URL.Path = "/path1/path2"
	if !Enabled(req) {
		t.Error("expect true, but got fasle")
	}

	req.URL.Path = "/path2"
	if !Enabled(req) {
		t.Error("expect true, but got false")
	}

	req.URL.Path = "/path2/path1"
	if Enabled(req) {
		t.Error("expect false, but got true")
	}
}
