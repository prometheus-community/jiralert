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

package clientset

import (
	"errors"
	"fmt"
	"sync"

	"github.com/andygrunwald/go-jira"
	"github.com/prometheus-community/jiralert/pkg/config"
)

type ClientSet struct {
	jira map[string]*jira.Client
	sync.RWMutex
}

var errorClientExists = errors.New("client already exists")

func (c *ClientSet) getJira(userName string) (*jira.Client, bool) {
	c.RLock()
	if c.jira == nil {
		c.jira = map[string]*jira.Client{}
	}
	jc, ok := c.jira[userName]
	c.RUnlock()

	return jc, ok
}

func (c *ClientSet) newJira(conf *config.ReceiverConfig) (*jira.Client, error) {
	if conf == nil {
		return nil, fmt.Errorf("missing receiver config")
	}

	if jc, ok := c.getJira(conf.User); ok {
		return jc, fmt.Errorf("jira %w: %s", errorClientExists, conf.User)
	}

	var (
		client *jira.Client
		err    error
	)
	if conf.User != "" && conf.Password != "" {
		tp := jira.BasicAuthTransport{
			Username: conf.User,
			Password: string(conf.Password),
		}
		client, err = jira.NewClient(tp.Client(), conf.APIURL)
	} else if conf.PersonalAccessToken != "" {
		tp := jira.PATAuthTransport{
			Token: string(conf.PersonalAccessToken),
		}
		client, err = jira.NewClient(tp.Client(), conf.APIURL)
	}

	if err == nil {
		c.Lock()
		c.jira[conf.User] = client
		c.Unlock()
	}

	return client, err
}

func (c *ClientSet) GetOrCreateJira(conf *config.ReceiverConfig) (*jira.Client, error) {
	jc, err := c.newJira(conf)
	if errors.Is(err, errorClientExists) {
		err = nil
	}
	return jc, err
}
