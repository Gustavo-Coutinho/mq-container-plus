/*
© Copyright IBM Corporation 2017, 2023

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ibm-messaging/mq-container/internal/command"
	"github.com/ibm-messaging/mq-container/pkg/logger"
	"github.com/ibm-messaging/mq-container/pkg/mqini"
)

// var debug = false
var log *logger.Logger

var collectDiagOnFail = false

func logTerminationf(format string, args ...interface{}) {
	logTermination(fmt.Sprintf(format, args...))
}

func logTermination(args ...interface{}) {
	msg := fmt.Sprint(args...)
	// Write the message to the termination log.  This is not the default place
	// that Kubernetes will look for termination information.
	log.Debugf("Writing termination message: %v", msg)
	err := ioutil.WriteFile("/run/termination-log", []byte(msg), 0660)
	if err != nil {
		log.Debug(err)
	}
	log.Error(msg)

	if collectDiagOnFail {
		logDiagnostics()
	}
}

func getLogFormat() string {
	logFormat := strings.ToLower(strings.TrimSpace(os.Getenv("MQ_LOGGING_CONSOLE_FORMAT")))
	//old-style env var is used.
	if logFormat == "" {
		logFormat = strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT")))
	}

	if logFormat != "" && (logFormat == "basic" || logFormat == "json") {
		return logFormat
	} else {
		//this is the case where value is either empty string or set to something other than "basic"/"json"
		logFormat = "basic"
	}

	return logFormat
}

// formatBasic formats a log message parsed from JSON, as "basic" text
func formatBasic(obj map[string]interface{}) string {
	// Emulate the MQ "MessageDetail=Extended" option, by appending inserts to the message
	// This is important for certain messages, where key details are only available in the extended message content
	inserts := make([]string, 0)
	for k, v := range obj {
		if strings.HasPrefix(k, "ibm_commentInsert") {
			inserts = append(inserts, fmt.Sprintf("%s(%v)", strings.Replace(k, "ibm_comment", "Comment", 1), obj[k]))
		} else if strings.HasPrefix(k, "ibm_arithInsert") {
			if v.(float64) != 0 {
				inserts = append(inserts, fmt.Sprintf("%s(%v)", strings.Replace(k, "ibm_arith", "Arith", 1), obj[k]))
			}
		}
	}
	sort.Strings(inserts)
	if len(inserts) > 0 {
		return fmt.Sprintf("%s %s [%v]\n", obj["ibm_datetime"], obj["message"], strings.Join(inserts, ", "))
	}
	// Convert time zone information from some logs (e.g. Liberty) for consistency
	obj["ibm_datetime"] = strings.Replace(obj["ibm_datetime"].(string), "+0000", "Z", 1)

	if obj["type"] == "liberty_message" {
		message := obj["message"].(string)
		if obj["ibm_messageId"] == nil {
			// Liberty logs at least one message without a message ID, but which spans multiple lines.
			// A missing message ID might imply more free-form text, so process the whitespace for better
			// visibility
			message = strings.ReplaceAll(strings.TrimSpace(message), "\n", "\n                         ")
		}
		return fmt.Sprintf("%s %s\n", obj["ibm_datetime"], message)
	} else if obj["type"] != nil && (obj["type"] == "liberty_trace") {
		timeStamp := obj["ibm_datetime"]
		threadID := ""
		srtModuleName := ""
		logLevel := ""
		ibmClassName := ""
		srtIbmClassName := ""
		ibmMethodName := ""
		message := ""

		if obj["loglevel"] != nil {
			//threadID is captured below
			if obj["ibm_threadId"] != nil {
				threadID = obj["ibm_threadId"].(string)
			}

			//logLevel character to be mirrored in console web server logging is decided below
			logLevelTmp := obj["loglevel"].(string)
			switch logLevelTmp {
			case "AUDIT":
				logLevel = "A"
			case "INFO":
				logLevel = "I"
			case "EVENT":
				logLevel = "1"
			case "ENTRY":
				logLevel = ">"
			case "EXIT":
				logLevel = "<"
			case "FINE":
				logLevel = "1"
			case "FINER":
				logLevel = "2"
			case "FINEST":
				logLevel = "3"
			default:
				logLevel = string(logLevelTmp[0])
			}

			//This is a 13 characters string present in extracted out of module node
			if obj["module"] != nil {
				srtModuleNameArr := strings.Split(obj["module"].(string), ".")
				arrLen := len(srtModuleNameArr)
				srtModuleName = srtModuleNameArr[arrLen-1]
				if len(srtModuleName) > 13 {
					srtModuleName = srtModuleName[0:13]
				}
			}
			if obj["ibm_className"] != nil {
				ibmClassName = obj["ibm_className"].(string)

				//A 13 character string is extracted from class name. This is required for FINE, FINER & FINEST log lines
				ibmClassNameArr := strings.Split(ibmClassName, ".")
				arrLen := len(ibmClassNameArr)
				srtIbmClassName = ibmClassNameArr[arrLen-1]
				if len(srtModuleName) > 13 {
					srtIbmClassName = srtIbmClassName[0:13]
				}
			}
			if obj["ibm_methodName"] != nil {
				ibmMethodName = obj["ibm_methodName"].(string)
			}
			if obj["message"] != nil {
				message = obj["message"].(string)
			}

			//For AUDIT & INFO logging
			if logLevel == "A" || logLevel == "I" {
				return fmt.Sprintf("%s %s %-13s %s %s %s %s\n", timeStamp, threadID, srtModuleName, logLevel, ibmClassName, ibmMethodName, message)
			}
			//For EVENT logLevel
			if logLevelTmp == "EVENT" {
				return fmt.Sprintf("%s %s %-13s %s %s\n", timeStamp, threadID, srtModuleName, logLevel, message)
			}
			//For ENTRY & EXIT
			if logLevel == ">" || logLevel == "<" {
				return fmt.Sprintf("%s %s %-13s %s %s %s\n", timeStamp, threadID, srtModuleName, logLevel, ibmMethodName, message)
			}
			//For deeper log levels
			if logLevelTmp == "FINE" || logLevel == "2" || logLevel == "3" {
				return fmt.Sprintf("%s %s %-13s %s %s %s %s\n", timeStamp, threadID, srtIbmClassName, logLevel, ibmClassName, ibmMethodName, message)
			}

		}
	}
	return fmt.Sprintf("%s %s\n", obj["ibm_datetime"], obj["message"])
}

// mirrorSystemErrorLogs starts a goroutine to mirror the contents of the MQ system error logs
func mirrorSystemErrorLogs(ctx context.Context, wg *sync.WaitGroup, mf mirrorFunc) (chan error, error) {
	// Always use the JSON log as the source
	return mirrorLog(ctx, wg, "/var/mqm/errors/AMQERR01.json", false, mf, false)
}

// mirrorQueueManagerErrorLogs starts a goroutine to mirror the contents of the MQ queue manager error logs
func mirrorQueueManagerErrorLogs(ctx context.Context, wg *sync.WaitGroup, name string, fromStart bool, mf mirrorFunc) (chan error, error) {
	// Always use the JSON log as the source
	qm, err := mqini.GetQueueManager(name)
	if err != nil {
		log.Debug(err)
		return nil, err
	}
	f := filepath.Join(mqini.GetErrorLogDirectory(qm), "AMQERR01.json")
	return mirrorLog(ctx, wg, f, fromStart, mf, true)
}

// mirrorHTPasswdLogs starts a goroutine to mirror the contents of the MQ HTPasswd authorization service's log
func mirrorHTPasswdLogs(ctx context.Context, wg *sync.WaitGroup, name string, fromStart bool, mf mirrorFunc) (chan error, error) {
	return mirrorLog(ctx, wg, "/var/mqm/errors/mqhtpass.json", false, mf, true)
}

// mirrorWebServerLogs starts a goroutine to mirror the contents of the Liberty web server messages.log
func mirrorWebServerLogs(ctx context.Context, wg *sync.WaitGroup, name string, fromStart bool, mf mirrorFunc) (chan error, error) {
	return mirrorLog(ctx, wg, "/var/mqm/web/installations/Installation1/servers/mqweb/logs/messages.log", false, mf, true)
}

func getDebug() bool {
	debug := os.Getenv("DEBUG")
	if debug == "true" || debug == "1" {
		return true
	}
	return false
}

func configureLogger(name string) (mirrorFunc, error) {
	var err error
	f := getLogFormat()
	d := getDebug()
	switch f {
	case "json":
		log, err = logger.NewLogger(os.Stderr, d, true, name)
		if err != nil {
			return nil, err
		}
		return func(msg string, isQMLog bool) bool {
			arrLoggingConsoleExcludeIds := strings.Split(strings.ToUpper(os.Getenv("MQ_LOGGING_CONSOLE_EXCLUDE_ID")), ",")
			if isExcludedMsgIdPresent(msg, arrLoggingConsoleExcludeIds) {
				//If excluded id is present do not mirror it, return back
				return false
			}
			// Check if the message is JSON
			if len(msg) > 0 && msg[0] == '{' {
				obj, err := processLogMessage(msg)
				if err == nil && isQMLog && filterQMLogMessage(obj) {
					return false
				}
				if err != nil {
					log.Printf("Failed to unmarshall JSON in log message - %v", msg)
				} else {
					fmt.Println(msg)
				}
			} else {
				// The log being mirrored isn't JSON, so wrap it in a simple JSON message
				// MQ error logs are usually JSON, but this is useful for Liberty logs - usually expect WLP_LOGGING_MESSAGE_FORMAT=JSON to be set when mirroring Liberty logs.
				fmt.Printf("{\"message\":\"%s\"}\n", msg)
			}
			return true
		}, nil
	case "basic":
		log, err = logger.NewLogger(os.Stderr, d, false, name)
		if err != nil {
			return nil, err
		}
		return func(msg string, isQMLog bool) bool {
			arrLoggingConsoleExcludeIds := strings.Split(strings.ToUpper(os.Getenv("MQ_LOGGING_CONSOLE_EXCLUDE_ID")), ",")
			if isExcludedMsgIdPresent(msg, arrLoggingConsoleExcludeIds) {
				//If excluded id is present do not mirror it, return back
				return false
			}
			// Check if the message is JSON
			if len(msg) > 0 && msg[0] == '{' {
				// Parse the JSON message, and print a simplified version
				obj, err := processLogMessage(msg)
				if err == nil && isQMLog && filterQMLogMessage(obj) {
					return false
				}
				if err != nil {
					log.Printf("Failed to unmarshall JSON in log message - %v", err)
				} else {
					fmt.Print(formatBasic(obj))
				}
			} else {
				// The log being mirrored isn't JSON, so just print it.
				// MQ error logs are usually JSON, but this is useful for Liberty logs - usually expect WLP_LOGGING_MESSAGE_FORMAT=JSON to be set when mirroring Liberty logs.
				fmt.Println(msg)
			}
			return true
		}, nil
	default:
		log, err = logger.NewLogger(os.Stdout, d, false, name)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("invalid value for LOG_FORMAT: %v", f)
	}
}

func processLogMessage(msg string) (map[string]interface{}, error) {
	var obj map[string]interface{}
	err := json.Unmarshal([]byte(msg), &obj)
	return obj, err
}

func filterQMLogMessage(obj map[string]interface{}) bool {
	hostname, err := os.Hostname()
	if os.Getenv("MQ_MULTI_INSTANCE") == "true" && err == nil && !strings.Contains(obj["host"].(string), hostname) {
		return true
	}
	return false
}

// Function to check if ids provided in MQ_LOGGING_CONSOLE_EXCLUDE_ID are present in given log line or not
func isExcludedMsgIdPresent(msg string, envExcludeIds []string) bool {
	for _, id := range envExcludeIds {
		if id != "" && strings.Contains(msg, strings.TrimSpace(id)) {
			return true
		}
	}
	return false
}

func logDiagnostics() {
	if getDebug() {
		log.Debug("--- Start Diagnostics ---")

		// show the directory ownership/permissions
		// #nosec G104
		out, _, _ := command.Run("ls", "-l", "/mnt/")
		log.Debugf("/mnt/:\n%s", out)
		// #nosec G104
		out, _, _ = command.Run("ls", "-l", "/mnt/mqm")
		log.Debugf("/mnt/mqm:\n%s", out)
		// #nosec G104
		out, _, _ = command.Run("ls", "-l", "/mnt/mqm/data")
		log.Debugf("/mnt/mqm/data:\n%s", out)
		// #nosec G104
		out, _, _ = command.Run("ls", "-l", "/mnt/mqm-log/log")
		log.Debugf("/mnt/mqm-log/log:\n%s", out)
		// #nosec G104
		out, _, _ = command.Run("ls", "-l", "/mnt/mqm-data/qmgrs")
		log.Debugf("/mnt/mqm-data/qmgrs:\n%s", out)
		// #nosec G104
		out, _, _ = command.Run("ls", "-l", "/var/mqm")
		log.Debugf("/var/mqm:\n%s", out)
		// #nosec G104
		out, _, _ = command.Run("ls", "-l", "/var/mqm/errors")
		log.Debugf("/var/mqm/errors:\n%s", out)
		// #nosec G104
		out, _, _ = command.Run("ls", "-l", "/etc/mqm")
		log.Debugf("/etc/mqm:\n%s", out)

		// Print out summary of any FDCs
		// #nosec G204
		cmd := exec.Command("/opt/mqm/bin/ffstsummary")
		cmd.Dir = "/var/mqm/errors"
		// #nosec G104
		outB, _ := cmd.CombinedOutput()
		log.Debugf("ffstsummary:\n%s", string(outB))

		log.Debug("---  End Diagnostics  ---")
	}
}

// Returns the value of MQ_LOGGING_CONSOLE_SOURCE environment variable
func getMQLogConsoleSource() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("MQ_LOGGING_CONSOLE_SOURCE")))

}

// Function to check if valid values are provided for environment variable MQ_LOGGING_CONSOLE_SOURCE. If not valid, main program throws a warning to console
func isLogConsoleSourceValid() bool {
	mqLogSource := getMQLogConsoleSource()
	retValue := false
	//If nothing is set, we will mirror all, so valid
	if mqLogSource == "" {
		return true
	}

	logConsoleSource := strings.Split(mqLogSource, ",")
	//This will find out if the environment variable contains permitted values and is comma separated
	for _, src := range logConsoleSource {
		switch strings.TrimSpace(src) {
		//If it is a permitted value, it is valid. Keep it as true, but dont return it. We may encounter something junk soon
		case "qmgr", "web", "":
			retValue = true
		//If invalid entry arrives in-between/anywhere, just return false, there is no turning back
		default:
			return false
		}
	}

	return retValue
}

// To check which all logs have to be mirrored
func checkLogSourceForMirroring(source string) bool {
	logsrcs := getMQLogConsoleSource()

	//Nothing set, this is when we mirror all
	if logsrcs == "" {
		return true
	}

	//Split the csv environment value so that we get an accurate comparison instead of a contains() check
	logSrcArr := strings.Split(logsrcs, ",")

	//Iterate through the array to decide on mirroring
	for _, arr := range logSrcArr {
		switch strings.TrimSpace(arr) {
		case "qmgr":
			//If value of source is qmgr and it exists in environment variable, mirror qmgr logs
			if source == "qmgr" {
				return true
			}
		case "web":
			//If value of source is web and it exists in environment variable, and mirror web logs
			if source == "web" {
				//If older environment variable is set make sure to print appropriate message
				if os.Getenv("MQ_ENABLE_EMBEDDED_WEB_SERVER_LOG") != "" {
					log.Println("Environment variable MQ_ENABLE_EMBEDDED_WEB_SERVER_LOG has now been replaced. Use MQ_LOGGING_CONSOLE_SOURCE instead.")
				}
				return true
			}
		}
	}
	return false
}
