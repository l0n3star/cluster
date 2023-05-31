package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	flag "github.com/spf13/pflag"
)

func main() {
	log.SetFlags(0)

	_, err := exec.LookPath("docker")
	if err != nil {
		log.Fatal("Please install docker.  Go to https://docs.docker.com/engine/install/")
	}

	makeDockerNetwork()

	// TODO:  support other linux OS besides ubuntu 20 for internal build
	// TODO:  support arm arch
	version := flag.StringP("version", "v", "", "CBS version")
	noinit := flag.Bool("noinit", false, "Do not initialize node")
	services := flag.StringP("services", "s", "", "Per node services")
	indexStorage := flag.StringP("index", "i", "plasma", "Index storage mode - plasma, memory_optimized")
	username := flag.StringP("username", "u", "admin", "Administrator username")
	password := flag.StringP("password", "p", "password", "Administrator password")
	sampleBucket := flag.StringP("bucket", "b", "", "Load sample bucket")

	flag.Parse()
	allNodeServices := []string{}

	if *version == "" {
		log.Fatal("Please provide CBS version")
	} else {
		re := regexp.MustCompile(`^(\d\.\d\.\d)-(\d{4,5})$`)
		if re.MatchString(*version) {
			matches := re.FindStringSubmatch(*version)
			buildImage(matches[1], matches[2])
		}
	}

	if !*noinit {
		if *services == "" {
			log.Fatal("Please specify per node services")
		} else {
			nodeServiceTuples := strings.Split(*services, ",")

			for _, t := range nodeServiceTuples {
				services := strings.Replace(strings.Split(t, ":")[1], "+", ",", -1)
				allNodeServices = append(allNodeServices, services)
			}
		}
	}

	port := getNextPort()
	name := randomName()
	makeNode(port, name+"0", *version)
	waitTillNodeIsUp(port)

	if *noinit {
		os.Exit(0)
	}

	initFirstNode(port, allNodeServices[0], *username, *password, *indexStorage)

	// Provision other nodes
	if len(allNodeServices) > 1 {
		IP := getIP(name + "0")
		allNodeStrings := fmt.Sprintf("ns_1@%s,", IP)

		for nodePort, i := port+1, 1; i < len(allNodeServices); i, nodePort = i+1, nodePort+1 {
			nodeServices := allNodeServices[i]
			nodeString := addNode(port, nodePort, fmt.Sprintf("%s%d", name, i), *version, nodeServices, *username, *password)
			allNodeStrings += nodeString
		}

		rebalance(port, allNodeStrings, *username, *password)

	}

	if *sampleBucket != "" {
		httpDo(http.MethodPost,
			fmt.Sprintf("http://localhost:%d/sampleBuckets/install", port),
			bytes.NewBufferString(fmt.Sprintf("[\"%s\"]", *sampleBucket)),
			Options{
				user:         *username,
				pass:         *password,
				errorMessage: "install sample bucket failed",
				status:       http.StatusAccepted,
			},
		)
	}

	log.Printf("First node of cluster - %s - is ready on port %d\n", name+"0", port)
}
