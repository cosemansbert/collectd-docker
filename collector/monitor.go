package collector

import (
	"errors"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/fsouza/go-dockerclient"
)

// Getenv is an utility function to get environment or default
func Getenv(env string, defaultValue string) string {
	var value = os.Getenv(env)
	if len(value) > 0 {
		return value
	}
	return defaultValue
}

var appLabel = Getenv("APP_LABEL_KEY", "app_id")
var appLocationLabel = "collectd_docker_app_label"
var taskLabel = Getenv("TASK_LABEL_KEY", "collectd_docker_task")
var taskLocationLabel = "collectd_docker_task_label"

var appEnvPrefix = Getenv("APP_ENV_KEY", "MARATHON_APP_ID") + "="
var appEnvLocationPrefix = "COLLECTD_DOCKER_APP_ENV="
var appEnvLocationTrimPrefix = "COLLECTD_DOCKER_APP_ENV_TRIM_PREFIX="
var taskEnvPrefix = Getenv("TASK_ENV_KEY", "MESOS_TASK_ID") + "="
var taskEnvLocationPrefix = "COLLECTD_DOCKER_TASK_ENV="
var taskEnvLocationTrimPrefix = "COLLECTD_DOCKER_TASK_ENV_TRIM_PREFIX="

const defaultTask = "default"

// ErrNoNeedToMonitor is used to skip containers
// that shouldn't be monitored by collectd
var ErrNoNeedToMonitor = errors.New("container is not supposed to be monitored")

// MonitorDockerClient represents restricted interface for docker client
// that is used in monitor, docker.Client is a subset of this interface
type MonitorDockerClient interface {
	InspectContainer(id string) (*docker.Container, error)
	Stats(opts docker.StatsOptions) error
}

// Monitor is responsible for monitoring of a single container (task)
type Monitor struct {
	client   MonitorDockerClient
	id       string
	app      string
	task     string
	tags     map[string]string
	interval int
}

// NewMonitor creates new monitor with specified docker client,
// container id and stat updating interval
func NewMonitor(c MonitorDockerClient, id string, interval int) (*Monitor, error) {
	tags := map[string]string{}
	container, err := c.InspectContainer(id)
	if err != nil {
		return nil, err
	}
	app := extractApp(container)

	if app == "" {
		log.Printf("No need to monitor %s %s\n", id, container.Name)
		return nil, ErrNoNeedToMonitor
	}
	extractTagsFromApp(tags, app)

	task := extractTask(container)
	extractTagsFromTask(tags, task)
	log.Printf("Monitoring for %s(%s) every %ds", app, task, interval)

	return &Monitor{
		client:   c,
		id:       container.ID,
		app:      app,
		task:     task,
		tags:     tags,
		interval: interval,
	}, nil
}

func (m *Monitor) handle(ch chan<- Stats) error {
	in := make(chan *docker.Stats)

	go func() {
		i := 0
		for s := range in {
			if i%m.interval != 0 {
				i++
				continue
			}

			ch <- Stats{
				Tags:  m.tags,
				Stats: *s,
			}

			i++
		}
	}()

	return m.client.Stats(docker.StatsOptions{
		ID:     m.id,
		Stats:  in,
		Stream: true,
	})
}

func extractApp(c *docker.Container) string {
	var app string

	location := extractMetadata(c, appLocationLabel, appEnvLocationPrefix, "")
	if location != "" {
		app = extractMetadata(c, location, location+"=", "")
	} else {
		app = extractMetadata(c, appLabel, appEnvPrefix, "")
	}

	prefix := extractEnv(c, appEnvLocationTrimPrefix)
	if prefix != "" {
		return strings.TrimPrefix(app, prefix)
	}

	return app
}

func extractTask(c *docker.Container) string {
	var task string

	location := extractMetadata(c, taskLocationLabel, taskEnvLocationPrefix, "")
	if location != "" {
		task = extractMetadata(c, location, location+"=", defaultTask)
	} else {
		task = extractMetadata(c, taskLabel, taskEnvPrefix, defaultTask)
	}

	prefix := extractEnv(c, taskEnvLocationTrimPrefix)
	if prefix != "" {
		return strings.TrimPrefix(task, prefix)
	}

	return task
}

func extractMetadata(c *docker.Container, label, envPrefix, missing string) string {
	if app, ok := c.Config.Labels[label]; ok {
		return app
	}

	env := extractEnv(c, envPrefix)
	if env != "" {
		return env
	}

	return missing
}

func extractEnv(c *docker.Container, envPrefix string) string {
	for _, e := range c.Config.Env {
		if strings.HasPrefix(e, envPrefix) {
			return strings.TrimPrefix(e, envPrefix)
		}
	}

	return ""
}

func extractTagsFromApp(tags map[string]string, app string) {
	tags["app_id"] = app
	split := strings.Split(app, "/")
	tags["app"] = split[len(split)-1]
	split = split[:len(split)-1]
	if len(split) <= 1 {
		tags["group"] = "/"
	} else {
		tags["group"] = strings.Join(split, "/")
		for i := 1; i < len(split); i++ {
			tags["group"+strconv.Itoa(i)] = strings.Join(split[:i+1], "/")
		}
	}
}

var splitregexp = regexp.MustCompile("[-.]")

func extractTagsFromTask(tags map[string]string, task string) {
	split := splitregexp.Split(task, -1)
	tags["task"] = task
	for i := 0; i < len(split); i++ {
		tags["task"+strconv.Itoa(i+1)] = split[i]
	}
}
