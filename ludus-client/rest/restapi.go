package rest

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"ludus/logger"

	"github.com/briandowns/spinner"
	resty "github.com/go-resty/resty/v2"
)

type ErrorStruct struct {
	Error string `json:"error"`
}

const APIBasePath = "/api/v2"

var user string

func InitClient(url string, apiKey string, proxy string, verify bool, debug bool, versionString string) *resty.Client {
	var client = resty.New()
	if debug {
		client.SetDebug(true)
		logger.InitLogger(debug)
	}
	if len(apiKey) > 0 && strings.Contains(apiKey, ".") {
		user = apiKey[:strings.IndexByte(apiKey, '.')]
	} else {
		user = "[No API key loaded]"
	}

	client.SetHeader("User-Agent", fmt.Sprintf("ludus-client/%s ", versionString))

	if apiKey != "" {
		client.SetHeader("X-API-KEY", apiKey)
	} else {
		logger.Logger.Fatal("No API key provided to InitClient")
	}

	if url != "" {
		client.SetBaseURL(url)
		logger.Logger.Debug("Endpoint URL: ", url)
	}

	if proxy != "" {
		client.SetProxy(proxy)
		logger.Logger.Debug("Endpoint Proxy: ", proxy)
	}

	if !verify {
		client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
		logger.Logger.Debug("Endpoint SSL Verify: ", verify)
	}

	client.OnRequestLog(func(r *resty.RequestLog) error {
		// mask API key header
		var apiKeyMasked string
		apiKey = r.Header.Get("X-API-KEY")
		if len(apiKey) > 4 && strings.Contains(apiKey, ".") {
			parts := strings.Split(apiKey, ".")
			if len(parts) == 2 && len(parts[1]) >= 10 {
				secondPart := parts[1]
				apiKeyMasked = parts[0] + "." + secondPart[:3] + "***REDACTED***" + secondPart[len(secondPart)-3:]
			} else {
				apiKeyMasked = parts[0] + ".***Less than 10 characters?***"
			}
		} else {
			apiKeyMasked = "API Key not set"
		}
		r.Header.Set("X-API-KEY", apiKeyMasked)
		return nil
	})

	return client
}

func prettyPrintError(errorString string) {

	if errorString == "Client sent an HTTP request to an HTTPS server." {
		logger.Logger.Error("Your Ludus server is using HTTPS. Make sure your URL includes 'https://'")
		return
	}

	var parsedError ErrorStruct
	err := json.Unmarshal([]byte(errorString), &parsedError)
	if err != nil {
		logger.Logger.Fatalf("%s\nCheck the IP/hostname and port in the URL provided to ludus to ensure it is correct.", errorString)
		return
	}

	logger.Logger.Error(parsedError.Error)
}

type PocketBaseErrorStruct struct {
	Data    any    `json:"data"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func prettyPrintPocketBaseError(errorBytes []byte) error {
	var parsedError PocketBaseErrorStruct
	err := json.Unmarshal(errorBytes, &parsedError)
	if err != nil {
		return fmt.Errorf("failed to parse PocketBase error: %w", err)
	}
	if parsedError.Message == "Something went wrong while processing your request." {
		logger.Logger.Error("Check the PocketBase logs for crash details")
	} else if parsedError.Message == "" {
		return fmt.Errorf("not an error from PocketBase")
	} else {
		logger.Logger.Error(parsedError.Message)
	}

	return nil
}

func processRESTResult(resp *resty.Response, err error) ([]byte, bool) {

	var result []byte
	var error bool = false

	if err != nil {
		logger.Logger.Fatal(err)
		error = true
	}

	if resp.StatusCode() == 403 || resp.StatusCode() == 409 || resp.StatusCode() == 404 || resp.StatusCode() == 413 {
		// Try to parse as PocketBase error first, then fall back to simple error format
		err := prettyPrintPocketBaseError(resp.Body())
		if err != nil {
			// Not a PocketBase error, try simple error format
			prettyPrintError(resp.String())
		}
		error = true
	}

	if resp.StatusCode() == 400 {
		// Try to parse as PocketBase error first, then fall back to simple error format
		err := prettyPrintPocketBaseError(resp.Body())
		if err != nil {
			// Not a PocketBase error, try simple error format
			prettyPrintError(resp.String())
		}
		error = true
	}

	if resp.StatusCode() == 401 {
		logger.Logger.Errorf("Error with request. Check your API key with --verbose")
		prettyPrintError(resp.String())
		error = true
	}

	if resp.StatusCode() == 500 || resp.StatusCode() == 502 {
		logger.Logger.Error("Error from server!")
		err := prettyPrintPocketBaseError(resp.Body())
		if err != nil {
			prettyPrintError(resp.String())
		}
		error = true
	}

	if error {
		return nil, false
	}

	if resp.StatusCode() == 200 || resp.StatusCode() == 201 {
		result = resp.Body()
	}

	return result, true
}

func GenericGet(client *resty.Client, apiPath string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}

	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().Get(apiPath)

	s.Stop()

	return processRESTResult(resp, err)
}

func GenericJSONPost(client *resty.Client, apiPath string, data any) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Post(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

func GenericDelete(client *resty.Client, apiPath string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().Delete(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

func GenericDeleteWithBody(client *resty.Client, apiPath string, data any) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Delete(apiPath)

	s.Stop()

	return processRESTResult(resp, err)
}

func GenericPutFile(client *resty.Client, apiPath string, data []byte) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetFileReader("file", "file", bytes.NewReader(data)).
		Put(apiPath)

	s.Stop()

	return processRESTResult(resp, err)
}

func PostFileAndForce(client *resty.Client, apiPath string, data []byte, filename string, force bool) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetFileReader("file", filename, bytes.NewReader(data)).
		SetFormData(map[string]string{
			"force": fmt.Sprintf("%t", force),
		}).
		Put(apiPath)

	s.Stop()

	return processRESTResult(resp, err)
}

func PostFileAndForceAndGlobal(client *resty.Client, apiPath string, data []byte, filename string, force bool, ansibleGlobal bool) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetFileReader("file", filename, bytes.NewReader(data)).
		SetFormData(map[string]string{
			"force":  fmt.Sprintf("%t", force),
			"global": fmt.Sprintf("%t", ansibleGlobal),
		}).
		Put(apiPath)

	s.Stop()

	return processRESTResult(resp, err)
}

func GenericJSONPut(client *resty.Client, apiPath string, data string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Put(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

func GenericJSONPatch(client *resty.Client, apiPath string, data string) ([]byte, bool) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(data).
		Patch(apiPath)

	s.Stop()

	return processRESTResult(resp, err)

}

func FileGet(client *resty.Client, apiPath string, outputPath string) {
	if !strings.HasPrefix(apiPath, APIBasePath) {
		apiPath = APIBasePath + apiPath
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Waiting for server..."
	s.Start()

	resp, err := client.R().Get(apiPath)

	s.Stop()

	if err != nil {
		logger.Logger.Fatal(err)
	}

	if resp.StatusCode() == 200 {
		err := os.WriteFile(outputPath, resp.Body(), 0644)
		if err != nil {
			logger.Logger.Fatalf("Failed to write file: %v", err)
		}
		logger.Logger.Infof("File downloaded and saved as %s", outputPath)
	} else {
		fmt.Printf("Received non-200 status code: %d\n", resp.StatusCode())
		fmt.Printf("Error: %s\n", string(resp.Body()))
	}
}
