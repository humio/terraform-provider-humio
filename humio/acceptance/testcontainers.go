package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/docker/go-connections/nat"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const humioJvmArgs = "-Xss2M"

func RunWithInstance(testFunc func(addr string, token string) int) {
	commonIdentifier := randomIdentifier()

	// Containers definitely run in the background
	ctx := context.Background()

	// Define network
	network, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{
			CheckDuplicate: true,
			Name:           commonIdentifier,
			Driver:         "bridge",
			SkipReaper:     false,
		},
	})

	if err != nil {
		log.Fatal("Could not create Docker network: ", err)
	}

	log.Println("Container network created: " + commonIdentifier)

	// Define containers
	hReq, hPort := humioRequest(commonIdentifier)

	// Start container(s) in order
	startedContainers := startContainers(ctx, hReq)

	// Expose mapped Humio port
	actualHPort, err := startedContainers[0].MappedPort(ctx, hPort)
	if err != nil {
		log.Fatal("Could not get mapped port of Humio container: ", err)
	}

	addr := fmt.Sprintf("http://localhost:%d", actualHPort.Int())

	// Fixed auth credentials
	token, err := fetchDeveloperToken(addr, commonIdentifier)
	if err != nil {
		log.Fatalf("Could not get token for user 'developer': %v", err)
	}

	// Run the actual tests
	log.Printf("Humio container running at %s", addr)
	returnCode := testFunc(addr, *token)

	// Stop containers after test run
	log.Println("Tearing down containers")
	for _, c := range startedContainers {
		_ = c.Terminate(ctx)
	}
	_ = network.Remove(ctx)

	os.Exit(returnCode)
}

func humioRequest(identifier string) (testcontainers.ContainerRequest, nat.Port) {
	port, _ := nat.NewPort("tcp", "8080")
	req := testcontainers.ContainerRequest{
		Name:         identifier,
		Image:        "humio/humio:1.18.4", // TODO: Support multiple versions?
		ExposedPorts: []string{port.Port()},
		Env: map[string]string{
			"HUMIO_JVM_ARGS":        humioJvmArgs,
			"AUTHENTICATION_METHOD": "single-user",
			"SINGLE_USER_PASSWORD":  identifier,
		},
		Networks:   []string{identifier},
		WaitingFor: wait.ForListeningPort(port),
	}

	return req, port
}

func startContainers(context context.Context, reqs ...testcontainers.ContainerRequest) []testcontainers.Container {
	var containers []testcontainers.Container
	for _, req := range reqs {
		c, err := testcontainers.GenericContainer(context, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})

		if err != nil {
			log.Fatal("Could not start container: "+req.Name, err)
		}

		containers = append(containers, c)
	}

	return containers
}

func fetchDeveloperToken(addr string, password string) (*string, error) {
	var token = ""

	payload := fmt.Sprintf(`{"login": "developer", "password": "%s"}`, password)
	path := fmt.Sprintf("%s%s", addr, "/api/v1/login")

	res, err := http.Post(path, "application/json", strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("could not perform developer login: %w", err)
	}

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("got a non-OK status code while logging in: %d", res.StatusCode)
	}

	m := make(map[string]interface{})
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("could not decode Humio login response: %w", err)
	}

	token = m["token"].(string)

	return &token, nil
}

func randomIdentifier() string {
	n := acctest.RandIntRange(1, 9999)
	return fmt.Sprintf("tf-acc-humio-%d", n)
}
