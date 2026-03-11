// Copyright 2017 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"net/http"
)

func limitRequests(maxConcurrent, maxQueue int, next http.Handler) http.Handler {
	if maxConcurrent <= 0 {
		panic("maxConcurrent must be > 0")
	}
	if maxQueue < 0 {
		panic("maxQueue must be >= 0")
	}

	slots := make(chan struct{}, maxConcurrent)
	queue := make(chan struct{}, maxQueue)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Step 1: enter bounded queue
		if maxQueue > 0 {
			select {
			case queue <- struct{}{}:
				defer func() { <-queue }()
			case <-r.Context().Done():
				return
			default:
				w.Header().Set("Retry-After", "5")
				http.Error(w, "server busy", http.StatusServiceUnavailable)
				return
			}
		}

		// Step 2: wait for an execution slot
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		case <-r.Context().Done():
			return
		}
	})
}
