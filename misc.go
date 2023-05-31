package main

import (
	"fmt"
	"log"
	"math/rand"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	runningContainers = []string{}
)

func randomName() string {
	rand.Seed(time.Now().UnixNano())
	return words[rand.Intn(len(words))]
}

func getNextPort() int {
	out, err := exec.Command("docker",
		"container",
		"ls",
		"--format",
		"{{.Ports}}",
		"--filter",
		"status=running").Output()
	if err != nil {
		cleanupContainers()
		log.Fatalf("could not list running containers: %v", err)
	}

	lastPort := 9000
	re := regexp.MustCompile(`:9\d{3}`)
	for _, line := range strings.Split(string(out), "\n") {
		matches := re.FindStringSubmatch(line)
		if len(matches) > 0 {
			port, err := strconv.Atoi(matches[0][1:])
			if err != nil {
				continue // silently ignore error
			}
			if port > lastPort {
				lastPort = port
			}
		}
	}

	return lastPort + 1
}

func getIP(containerID string) string {
	out, err := exec.Command("docker",
		"inspect",
		"-f",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerID).Output()

	if err != nil {
		cleanupContainers()
		log.Fatalf("could not get IP of container %s: %v", containerID, err)
	}

	return fmt.Sprint(strings.TrimSpace(string(out)))
}

// if network cb-net does not exist, create it
func makeDockerNetwork() {
	cmd := exec.Command("docker", "network", "inspect", "cb-net")

	if err := cmd.Run(); err != nil {
		cmd := exec.Command("docker", "network", "create", "cb-net")

		if err := cmd.Run(); err != nil {
			log.Fatalf("could not create docker network cb-net: %v", err)
		}

		log.Println("created docker network cb-net")
	}
}

func cleanupContainers() {
	containers := strings.Join(runningContainers[:], " ")
	cmd := exec.Command(
		"docker",
		"stop",
		containers,
	)

	if err := cmd.Run(); err != nil {
		log.Fatalf("could not remove containers %s:\n%v", containers, err)
	}
}
