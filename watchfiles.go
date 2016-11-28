// Package watchfiles provides a reusable utility to watch files and get notified of changes
package watchfiles

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"gopkg.in/fsnotify.v1"
)

var logger = log.New(os.Stderr, "watchfiles: ", log.LstdFlags|log.Lmicroseconds)

// SetLog allows customizing the logger
func SetLog(l *log.Logger) {
	logger = l
}

// WatchFiles allows loading, and optionally watching files for changes.
type WatchFiles struct {
	path        string
	filePattern *regexp.Regexp
	updateCb    UpdateCallback
	removeCb    RemoveCallback

	eventsLock *sync.Mutex
	eventsMap  map[string]fsnotify.Event
}

// UpdateCallback gets invoked when a watched file is modified,
// or when a new file is added to a watched directory
type UpdateCallback func(filePath string, reader *bufio.Reader)

// RemoveCallback gets invoked when a watched file is modified
type RemoveCallback func(filePath string)

// NewWatchFiles is the constructor function for a Config instance
//
//	The following parameters are supported:
//		filePath: the directory or file to open and watch. If a directory is used, all files in it are loaded
//		updateCb: (optional) a callback that is invoked when config data files are updated, or added
//		removeCb: (optional) a callback that is invoked when config data files are removed
func NewWatchFiles(filePath string, filePattern *regexp.Regexp, updateCb UpdateCallback,
	removeCb RemoveCallback) (*WatchFiles, error) {

	c := WatchFiles{path: filePath, filePattern: filePattern}
	c.updateCb = updateCb
	c.removeCb = removeCb

	c.eventsLock = &sync.Mutex{}
	c.eventsMap = make(map[string]fsnotify.Event)

	var err = c.loadFiles(filePath)
	if err != nil {
		return nil, err
	}

	c.watchFile(filePath)
	return &c, nil
}

func (wf *WatchFiles) loadFiles(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		logger.Printf("ERROR - Unable to open: %s", filePath)
		return err
	}

	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		logger.Printf("ERROR - Unable to stat: %s", filePath)
		return err
	}

	if fi.IsDir() {
		list, err := f.Readdir(-1)
		if err != nil {
			logger.Printf("ERROR - Unable to read directory: %s", fi.Name())
			return err
		}

		for _, dirFile := range list {
			if dirFile.IsDir() {
				logger.Printf("INFO - Skipping sub-directory %s ", fi.Name())
				continue
			}

			var fileName = filepath.Join(f.Name(), dirFile.Name())
			wf.openAndloadFile(fileName)
		}
	} else {
		wf.loadFile(f)
	}

	return nil
}

func (wf *WatchFiles) openAndloadFile(filePath string) {
	logger.Printf("INFO - Processsing file: %s", filePath)

	configFile, err := os.Open(filePath)
	if err != nil {
		logger.Printf("ERROR - %v", err)
		return
	}

	defer configFile.Close()
	wf.loadFile(configFile)
}

func (wf *WatchFiles) loadFile(configFile *os.File) {
	rdr := bufio.NewReader(configFile)
	logger.Printf("loaded data from: %s", configFile.Name())

	if wf.updateCb != nil {
		wf.updateCb(configFile.Name(), rdr)
	}
}

func (wf *WatchFiles) watchFile(filePath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Printf("ERROR - %v", err)
	}

	go func() {
		for {
			timer := time.NewTimer(1 * time.Second)
			select {
			case event := <-watcher.Events:
				fn := filepath.Base(event.Name)
				if !wf.filePattern.MatchString(fn) {
					logger.Printf("INFO - ignoring file %s as it does not match name pattern", event.Name)
					continue
				}

				logger.Printf("INFO - Received file watch event: %s", event.String())
				wf.storeEvent(event)
				timer.Stop()

			case <-timer.C:
				wf.processEvents()

			case err := <-watcher.Errors:
				logger.Printf("error: %v from file watcher: %s", err, filePath)
			}
		}
	}()

	err = watcher.Add(filePath)
	if err != nil {
		logger.Printf("ERROR - %v", err)
		return
	}

	logger.Printf("INFO - Watching file: %s", filePath)
}

func (wf *WatchFiles) storeEvent(event fsnotify.Event) {
	wf.eventsLock.Lock()
	defer wf.eventsLock.Unlock()

	wf.eventsMap[event.Name] = event
	logger.Printf("INFO - %d pending events", len(wf.eventsMap))
}

func (wf *WatchFiles) processEvents() {
	wf.eventsLock.Lock()
	defer wf.eventsLock.Unlock()

	if len(wf.eventsMap) == 0 {
		return
	}

	logger.Printf("INFO - Processing %d events", len(wf.eventsMap))

	for _, event := range wf.eventsMap {
		delete(wf.eventsMap, event.Name)
		if ((event.Op & fsnotify.Write) == fsnotify.Write) || ((event.Op & fsnotify.Chmod) == fsnotify.Chmod) {
			logger.Printf("INFO - Modified file: %s", event.Name)
			wf.openAndloadFile(event.Name)
		} else if (event.Op & fsnotify.Create) == fsnotify.Create {
			logger.Printf("INFO - Created file: %s", event.Name)
			wf.openAndloadFile(event.Name)
		} else if (event.Op & fsnotify.Remove) == fsnotify.Remove {
			logger.Printf("INFO - Deleted file: %s", event.Name)
			if wf.removeCb != nil {
				wf.removeCb(event.Name)
			}
		} else if (event.Op & fsnotify.Rename) == fsnotify.Rename {
			logger.Printf("INFO - Renamed file: %s", event.Name)
			wf.openAndloadFile(event.Name)
		}
	}
}
