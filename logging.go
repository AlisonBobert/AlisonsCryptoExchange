package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LogType string

const (
	LogTypeActivity LogType = "ACTIVITY"
	LogTypeError    LogType = "ERROR"
)

var (
	activityLog *log.Logger
	errorLog    *log.Logger
	once        sync.Once
)

func InitLogging(logDir string) error {
	var initErr error
	once.Do(func() {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			initErr = fmt.Errorf("failed to create log directory: %v", err)
			return
		}

		activityFile, err := os.OpenFile(
			filepath.Join(logDir, "activity.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY,
			0644,
		)
		if err != nil {
			initErr = fmt.Errorf("failed to open activity log: %v", err)
			return
		}

		errorFile, err := os.OpenFile(
			filepath.Join(logDir, "errors.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY,
			0644,
		)
		if err != nil {
			initErr = fmt.Errorf("failed to open error log: %v", err)
			return
		}

		activityLog = log.New(activityFile, "", 0)
		errorLog = log.New(errorFile, "", 0)
	})

	return initErr
}

func Log(logType LogType, message string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := fmt.Sprintf("[%s] [%s] %s", timestamp, logType, message)

	switch logType {
	case LogTypeActivity:
		if activityLog != nil {
			activityLog.Println(logLine)
		}
	case LogTypeError:
		if errorLog != nil {
			errorLog.Println(logLine)
			log.Println(logLine)
		}
	default:
		log.Printf("Unknown log type: %s - Message: %s", logType, message)
	}
}

func LogActivity(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	Log(LogTypeActivity, message)
}

func LogError(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	Log(LogTypeError, message)
}
