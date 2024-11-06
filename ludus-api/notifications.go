//

//

package ludusapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/containrrr/shoutrrr"
	shoutrrrTypes "github.com/containrrr/shoutrrr/pkg/types"
	"golang.org/x/sync/errgroup"
	yaml "sigs.k8s.io/yaml"
)

type Payload struct {
	Host     string        `json:"host,omitempty"` // host (optional)
	RangeID  string        `json:"range_id"`       // range id
	Test     bool          `json:"test"`           // false
	Duration time.Duration `json:"duration"`       // duration of the ansible playbook run

	//private, populated during init (marked as Public for JSON serialization)
	Date    string `json:"date"` //populated by Send function.
	Subject string `json:"subject"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type Notify struct {
	ConfigFilePath string
	Payload        Payload
}

func NewPayload(success bool, rangeID string, ansibleOutput string, test bool, duration time.Duration) Payload {
	payload := Payload{
		Host:    ServerConfiguration.ProxmoxNode,
		RangeID: rangeID,
		Test:    test,
	}

	payload.Success = success
	payload.Duration = duration
	payload.Date = time.Now().Format(time.RFC3339)
	payload.Subject = payload.GenerateSubject()
	payload.Message = payload.GenerateMessage(ansibleOutput)
	return payload
}

func (p *Payload) GenerateSubject() string {
	//generate a detailed failure message
	var subject string
	if p.Success {
		subject = fmt.Sprintf("Deployment succeeded for range %s on host %s", p.RangeID, p.Host)
	} else {
		subject = fmt.Sprintf("Error while deploying range %s on host %s", p.RangeID, p.Host)
	}
	return subject
}

func (p *Payload) GenerateMessage(ansibleOutput string) string {
	//generate a detailed failure message

	messageParts := []string{}

	if p.Success {
		messageParts = append(messageParts, fmt.Sprintf("Successfully deployed range: %s", p.RangeID))
	} else {
		messageParts = append(messageParts, fmt.Sprintf("Error while deploying range: %s", p.RangeID))
		fatalErrors := getFatalErrorsFromString(ansibleOutput)
		if len(fatalErrors) > 0 {
			messageParts = append(messageParts, "Fatal errors:")
			for errorCount, fatalError := range fatalErrors {
				messageParts = append(messageParts, fmt.Sprintf("Error %d:\n%s\n", errorCount+1, fatalError))
			}
		}

	}
	if len(p.Host) > 0 {
		messageParts = append(messageParts, fmt.Sprintf("Host: %s", p.Host))
	}

	messageParts = append(messageParts,
		fmt.Sprintf("Duration: %s", p.Duration.Round(time.Second)),
		"",
		fmt.Sprintf("Date: %s", p.Date),
	)

	if p.Test {
		messageParts = append([]string{"TEST NOTIFICATION:"}, messageParts...)
	}

	return strings.Join(messageParts, "\n")
}

func (n *Notify) Send() error {
	bytes, err := os.ReadFile(n.ConfigFilePath)
	if err != nil {
		return fmt.Errorf("can't read %s: %v", n.ConfigFilePath, err)
	}
	//retrieve list of notification endpoints from config file
	type NotificationConfig struct {
		Notify struct {
			Urls []string `json:"urls"`
		} `json:"notify"`
	}
	var config NotificationConfig
	err = yaml.Unmarshal(bytes, &config)
	if err != nil {
		return err
	}

	configUrls := config.Notify.Urls

	if len(configUrls) == 0 {
		return nil
	}

	//remove http:// https:// and script:// prefixed urls
	notifyWebhooks := []string{}
	notifyShoutrrr := []string{}

	for ndx := range configUrls {
		if strings.HasPrefix(configUrls[ndx], "https://") || strings.HasPrefix(configUrls[ndx], "http://") {
			notifyWebhooks = append(notifyWebhooks, configUrls[ndx])
		} else {
			notifyShoutrrr = append(notifyShoutrrr, configUrls[ndx])
		}
	}

	//run all webhooks and shoutrr commands in parallel
	//var wg sync.WaitGroup
	var eg errgroup.Group

	for _, url := range notifyWebhooks {
		// execute collection in parallel go-routines
		_url := url
		eg.Go(func() error { return n.SendWebhookNotification(_url) })
	}
	for _, url := range notifyShoutrrr {
		// execute collection in parallel go-routines
		_url := url
		eg.Go(func() error { return n.SendShoutrrrNotification(_url) })
	}

	//and wait for completion, error or timeout.
	if err := eg.Wait(); err == nil {
		return nil
	} else {
		return err
	}
}

func (n *Notify) SendWebhookNotification(webhookUrl string) error {
	log.Printf("Sending Webhook to %s", webhookUrl)
	requestBody, err := json.Marshal(n.Payload)
	if err != nil {
		log.Printf("An error occurred while sending Webhook to %s: %v", webhookUrl, err)
		return err
	}

	resp, err := http.Post(webhookUrl, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Printf("An error occurred while sending Webhook to %s: %v", webhookUrl, err)
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (n *Notify) SendShoutrrrNotification(shoutrrrUrl string) error {

	sender, err := shoutrrr.CreateSender(shoutrrrUrl)
	if err != nil {
		log.Printf("An error occurred while sending notifications %v: %v", shoutrrrUrl, err)
		return err
	}

	serviceName, params, err := n.GenShoutrrrNotificationParams(shoutrrrUrl)

	if err != nil {
		log.Printf("An error occurred while generating notification payload for %s %s:\n %v", serviceName, shoutrrrUrl, err)
		return err
	}

	errs := sender.Send(n.Payload.Message, params)
	if len(errs) > 0 {
		var errstrings []string

		for _, err := range errs {
			if err == nil || err.Error() == "" {
				continue
			}
			errstrings = append(errstrings, err.Error())
		}
		//sometimes there are empty errs, we're going to skip them.
		if len(errstrings) == 0 {
			return nil
		} else {
			log.Printf("One or more errors occurred while sending notifications for %s:", shoutrrrUrl)
			log.Print(errs)
			return errors.New(strings.Join(errstrings, "\n"))
		}
	}
	return nil
}

func (n *Notify) GenShoutrrrNotificationParams(shoutrrrUrl string) (string, *shoutrrrTypes.Params, error) {
	serviceURL, err := url.Parse(shoutrrrUrl)
	if err != nil {
		return "", nil, err
	}

	serviceName := serviceURL.Scheme
	params := &shoutrrrTypes.Params{}

	logoUrl := "https://docs.ludus.cloud/img/logo.png"
	subject := n.Payload.Subject
	switch serviceName {
	// no params supported for these services
	case "hangouts", "mattermost", "teams", "rocketchat":
		break
	case "discord":
		(*params)["title"] = subject
	case "gotify":
		(*params)["title"] = subject
	case "ifttt":
		(*params)["title"] = subject
	case "join":
		(*params)["title"] = subject
		(*params)["icon"] = logoUrl
	case "ntfy":
		(*params)["title"] = subject
		(*params)["icon"] = logoUrl
	case "opsgenie":
		(*params)["title"] = subject
	case "pushbullet":
		(*params)["title"] = subject
	case "pushover":
		(*params)["title"] = subject
	case "slack":
		(*params)["title"] = subject
	case "smtp":
		(*params)["subject"] = subject
	case "standard":
		(*params)["subject"] = subject
	case "telegram":
		(*params)["title"] = subject
	case "zulip":
		(*params)["topic"] = subject
	}

	return serviceName, params, nil
}

func getFatalErrorsFromString(input string) []string {
	scanner := bufio.NewScanner(strings.NewReader(input))
	fatalRegex := regexp.MustCompile(`^fatal:.*$|^failed:.*$|^ERROR! .*$`)
	ignoreRegex := regexp.MustCompile(`\.\.\.ignoring$`)
	errorCount := 0
	fatalErrors := []string{}
	var threeLinesAgo string
	var twoLinesAgo string
	var previousLine string
	for scanner.Scan() {
		currentLine := scanner.Text()
		// Check if the current line is an ignoring line and the previous line was a fatal line
		if ignoreRegex.MatchString(currentLine) && fatalRegex.MatchString(previousLine) {
			// Skip this fatal line because it's followed by ...ignoring
			previousLine = "" // Reset previousLine to avoid false positives
			continue
		}

		if fatalRegex.MatchString(previousLine) {
			// This means the previous line was a fatal line not followed by ...ignoring
			// Check if this is 'TASK [Promote this server to Additional DC 2]' which is known to fail without ...ignoring
			if strings.Contains(previousLine, "Unhandled exception while executing module: Verification of prerequisites for Domain Controller promotion failed. Role change is in progress or this computer needs to be restarted") && strings.Contains(threeLinesAgo, "TASK [Promote this server to Additional DC 2]") {
				continue
			}
			errorCount += 1
			fatalErrors = append(fatalErrors, formatError(previousLine))
		}

		// Update previous lines for the next iteration
		threeLinesAgo = twoLinesAgo
		twoLinesAgo = previousLine
		previousLine = currentLine
	}

	// Check the last line in case the file ends with a fatal line
	if fatalRegex.MatchString(previousLine) {
		errorCount += 1
		fatalErrors = append(fatalErrors, formatError(previousLine))
	}
	return fatalErrors
}

func formatError(errorLine string) string {
	formattedLine := strings.ReplaceAll(errorLine, "\\r\\n", "\n")
	formattedLine = strings.ReplaceAll(formattedLine, "\\n", "\n")
	return formattedLine
}
