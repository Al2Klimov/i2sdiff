package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"
)

type checkableName struct {
	host, service string
}

type checkable struct {
	total   uint64
	missing map[string]struct{}
}

type zone struct {
	total, missing uint64
	checkables     map[checkableName]*checkable
}

func main() {
	user := flag.String("user", "root", "USERNAME")
	pass := flag.String("pass", "", "PASSWORD")
	addr := flag.String("addr", "localhost:5665", "HOST:PORT")
	flag.Parse()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	wg := &sync.WaitGroup{}
	authn := url.UserPassword(*user, *pass)
	configOwners := map[string]struct{}{}

	var sds struct {
		Results []struct {
			Attrs struct {
				HostName    string `json:"host_name"`
				ServiceName string `json:"service_name"`
				Zone        string `json:"zone"`
			} `json:"attrs"`
			Name string `json:"name"`
		} `json:"results"`
	}

	wg.Add(2)

	go func() {
		defer wg.Done()

		assert(jsonReq(
			client,
			&http.Request{
				Method: "GET",
				URL: &url.URL{
					Scheme: "https",
					User:   authn,
					Host:   *addr,
					Path:   "/v1/objects/scheduleddowntimes",
				},
			},
			map[string]interface{}{
				"attrs": []string{"host_name", "service_name", "zone"},
			},
			&sds,
		))
	}()

	go func() {
		defer wg.Done()

		var ds struct {
			Results []struct {
				Attrs struct {
					ConfigOwner string `json:"config_owner"`
				} `json:"attrs"`
			} `json:"results"`
		}

		assert(jsonReq(
			client,
			&http.Request{
				Method: "GET",
				URL: &url.URL{
					Scheme: "https",
					User:   authn,
					Host:   *addr,
					Path:   "/v1/objects/downtimes",
				},
			},
			map[string]interface{}{"attrs": []string{}},
			&ds,
		))

		for _, dt := range ds.Results {
			configOwners[dt.Attrs.ConfigOwner] = struct{}{}
		}
	}()

	wg.Wait()

	zones := map[string]*zone{}
	for _, sd := range sds.Results {
		z, ok := zones[sd.Attrs.Zone]
		if !ok {
			z = &zone{checkables: map[checkableName]*checkable{}}
			zones[sd.Attrs.Zone] = z
		}

		chkName := checkableName{sd.Attrs.HostName, sd.Attrs.ServiceName}

		chk, ok := z.checkables[chkName]
		if !ok {
			chk = &checkable{missing: map[string]struct{}{}}
			z.checkables[chkName] = chk
		}

		z.total++
		chk.total++

		if _, ok := configOwners[sd.Name]; !ok {
			chk.missing[sd.Name] = struct{}{}
			z.missing++
		}
	}

	for zName, z := range zones {
		if z.missing > 0 {
			fmt.Printf("Zone %#v (%d/%d):\n\n", zName, z.missing, z.total)

			for chkName, chk := range z.checkables {
				if len(chk.missing) > 0 {
					name := chkName.host
					if chkName.service != "" {
						name += "!" + chkName.service
					}

					fmt.Printf("  Checkable %#v (%d/%d):\n", name, len(chk.missing), chk.total)

					for sd := range chk.missing {
						fmt.Printf("    ScheduledDowntime %#v\n", sd)
					}
				}
			}
		}
	}
}

func jsonReq(client *http.Client, req *http.Request, in, out interface{}) error {
	params := &bytes.Buffer{}
	if err := json.NewEncoder(params).Encode(in); err != nil {
		return err
	}

	req.Body = ioutil.NopCloser(params)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%d", resp.StatusCode)
	}

	return json.NewDecoder(bufio.NewReader(resp.Body)).Decode(out)
}

func assert(err error) {
	if err != nil {
		panic(err)
	}
}
