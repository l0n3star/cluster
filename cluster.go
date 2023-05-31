package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const (
	MAX_TRIES = 120
)

type Options struct {
	user, pass, errorMessage string
	status                   int
}

func httpDo(method, uri string, data *bytes.Buffer, opt Options) []byte {
	// cleanup before panic
	defer func() {
		if p := recover(); p != nil {
			cleanupContainers()
			panic(p)
		}
	}()

	client := &http.Client{}
	req, err := http.NewRequest(method, uri, data)

	if err != nil {
		cleanupContainers()
		log.Fatalf("%s\nNewRequest failed for %+v: %v", opt.errorMessage, req, err)
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	if opt.user != "" && opt.pass != "" {
		req.SetBasicAuth(opt.user, opt.pass)
	}

	req.Close = true
	resp, err := client.Do(req)
	if err != nil {
		cleanupContainers()
		log.Fatalf("%s\nsend request failed %+v: %v", opt.errorMessage, req, err)
	}

	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		cleanupContainers()
		log.Fatalf("%s\ncould not read resp body for request %+v: %v", opt.errorMessage, req, err)
	}

	if resp.StatusCode != opt.status {
		rb, err := req.GetBody()
		if err != nil {
			cleanupContainers()
			log.Fatalf("%s\ncould not get req body for request %+v: %v", opt.errorMessage, req, err)
		}
		reqBody, err := io.ReadAll(rb)
		if err != nil {
			cleanupContainers()
			log.Fatalf("%s\ncould not read req body for request %+v: %v", opt.errorMessage, req, err)
		}

		cleanupContainers()
		log.Fatalf("%s\n%+v\nreqBody: %s\n%+v\nrespBody: %s", opt.errorMessage, req, string(reqBody), resp, string(respBody))
	}

	return respBody
}

func makeNode(port int, containerName string, version string) {
	cmd := exec.Command(
		"docker", "run", "--rm", "-d",
		"-p", fmt.Sprintf("%d:8091", port),
		"-p", "8092-8096",
		"-p", "11207",
		"-p", "11210",
		"-p", "18091-18096",
		"--name", containerName,
		"--privileged",
		"--network=cb-net",
		fmt.Sprintf("couchbase/server:%s", version),
	)
	if err := cmd.Run(); err != nil {
		log.Fatalf("could not create node: %v", err)
	}

	runningContainers = append(runningContainers, containerName)
}

func waitTillNodeIsUp(port int) {
	url := fmt.Sprintf("http://localhost:%d", port)

	for tries := 1; tries <= MAX_TRIES; tries++ {
		resp, err := http.Get(url)
		if resp != nil && err != nil {
			log.Printf("HTTP GET for waitTillNodeIsUp failed: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if resp != nil && resp.StatusCode == http.StatusOK {
			return
		}

		time.Sleep(1 * time.Second)
	}

	cleanupContainers()
	log.Fatal("node is not up in 2 min")
}

func initFirstNode(port int, services, username, password, storageMode string) {
	httpDo(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/settings/indexes", port),
		bytes.NewBufferString(url.Values{"storageMode": {storageMode}}.Encode()),
		Options{
			errorMessage: "Index configuration failed",
			status:       http.StatusOK,
		},
	)

	httpDo(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/node/controller/setupServices", port),
		bytes.NewBufferString(url.Values{"services": {services}}.Encode()),
		Options{
			errorMessage: "Setting up services failed",
			status:       http.StatusOK,
		},
	)

	httpDo(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/settings/web", port),
		bytes.NewBufferString(url.Values{"username": {username}, "password": {password}, "port": {"8091"}}.Encode()),
		Options{
			errorMessage: "Setting up username and password failed",
			status:       http.StatusOK,
		},
	)

	// Set memory quotas
	// n1ql and backup don't have quotas
	form := url.Values{}
	form.Add("memoryQuota", "256")
	form.Add("indexMemoryQuota", "256")
	form.Add("cbasMemoryQuota", "1024")
	form.Add("eventingMemoryQuota", "256")
	form.Add("ftsMemoryQuota", "256")

	httpDo(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/pools/default", port),
		bytes.NewBufferString(form.Encode()),
		Options{
			user:         username,
			pass:         password,
			errorMessage: "Setting up memory quotas failed",
			status:       http.StatusOK,
		},
	)
}

func addNode(port, nodePort int, hostname, version, services, username, password string) string {
	makeNode(nodePort, hostname, version)
	waitTillNodeIsUp(nodePort)
	IP := getIP(hostname)

	form := url.Values{}
	form.Add("hostname", IP)
	form.Add("user", username)
	form.Add("password", password)
	form.Add("services", services)

	httpDo(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/controller/addNode", port),
		bytes.NewBufferString(form.Encode()),
		Options{
			user:         username,
			pass:         password,
			errorMessage: "Add node failed",
			status:       http.StatusOK,
		},
	)

	return fmt.Sprintf("ns_1@%s,", IP)
}

func rebalance(port int, knownNodes, username, password string) {
	nodes := url.QueryEscape(strings.TrimSuffix(knownNodes, ","))
	form := url.Values{}
	form.Add("knownNodes", nodes)

	httpDo(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/controller/rebalance", port),
		bytes.NewBufferString(strings.ReplaceAll(form.Encode(), "%25", "%")),
		Options{
			user:         username,
			pass:         password,
			errorMessage: "Rebalance failed",
			status:       http.StatusOK,
		},
	)

	for tries := 1; tries <= MAX_TRIES; tries++ {
		body := httpDo(http.MethodGet,
			fmt.Sprintf("http://localhost:%d/pools/default/rebalanceProgress", port),
			bytes.NewBufferString(""),
			Options{
				user:         username,
				pass:         password,
				errorMessage: "Could not get rebalance progress",
				status:       http.StatusOK,
			},
		)

		progress := struct {
			Status string `json:"status"`
		}{}

		if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&progress); err != nil {
			cleanupContainers()
			log.Fatalf("Could not decode rebalance progress: %v", err)
		}

		if progress.Status == "none" {
			return
		}

		time.Sleep(1 * time.Second)
	}

	cleanupContainers()
	log.Fatal("Could not rebalance in 2 min")
}
