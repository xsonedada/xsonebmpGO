package security

import (
    "log"
    "os"
    "sync"
)

type Logger struct {
    info     *log.Logger
    warn     *log.Logger
    critical *log.Logger
    file     *os.File
    mu       sync.Mutex
}

var SecurityLogger *Logger

func InitLogger(logFile string) (*Logger, error) {
    f, err := os.OpenFile(logFile, 
        os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }
    
    SecurityLogger = &Logger{
        info:     log.New(f, "INFO: ", log.LstdFlags),
        warn:     log.New(f, "WARN: ", log.LstdFlags),
        critical: log.New(f, "CRITICAL: ", log.LstdFlags),
        file:     f,
    }
    
    return SecurityLogger, nil
}

func (l *Logger) Info(msg string, args ...interface{}) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.info.Printf(msg+" %v", args)
}

func (l *Logger) Warn(msg string, args ...interface{}) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.warn.Printf(msg+" %v", args)
}

func (l *Logger) Critical(msg string, args ...interface{}) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.critical.Printf(msg+" %v", args)
}

func (l *Logger) Close() {
    l.file.Close()
}