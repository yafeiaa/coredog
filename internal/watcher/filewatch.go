package watcher

// Listen some dir and call CoreFileService.Pub to publish the corefile infomation to CorefileService.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type FileWatcher struct {
	watch          *fsnotify.Watcher
	receiver       chan string
	processedFiles map[string]bool // Track processed files to avoid duplicates
}

func NewFileWatcher(recevier chan string) *FileWatcher {
	w := new(FileWatcher)
	w.watch, _ = fsnotify.NewWatcher()
	w.receiver = recevier
	w.processedFiles = make(map[string]bool)
	return w
}

func pathExist(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isDir(path string) bool {
	s, err := os.Stat(path)
	if err != nil {

		return false
	}
	return s.IsDir()

}

func (fw *FileWatcher) Watch(dir string) error {
	ok, err := pathExist(dir)
	if err != nil {
		return errors.Wrapf(err, "unexcepted error")
	}
	if !ok {
		return fmt.Errorf("dir is not exist:%s", dir)
	}
	if !isDir(dir) {
		return fmt.Errorf("input path is not a valid dir:%s", dir)
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			path, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			// Set directory permissions to 777
			if err := os.Chmod(path, 0777); err != nil {
				logrus.Warnf("failed to set permissions for directory %s: %v", path, err)
			}
			err = fw.watch.Add(path)
			if err != nil {
				return err
			}
			logrus.Infof("started watch corefile in:%s", path)
		}
		return nil
	})
	go fw.watchEvents()
	return nil
}

func (fw *FileWatcher) watchEvents() {
	for {
		select {
		case ev := <-fw.watch.Events:
			{
				logrus.Infof("received fsnotify event: %s, op: %s", ev.Name, ev.Op.String())
				// Handle CREATE or WRITE events for new files
				if ev.Op&fsnotify.Create == fsnotify.Create || ev.Op&fsnotify.Write == fsnotify.Write {
					file, err := os.Stat(ev.Name)
					if err != nil {
						logrus.Errorf("received a create/write event from fsnotify,but os.stat error:%s", err.Error())
					} else {
						if file.IsDir() {
							if ev.Op&fsnotify.Create == fsnotify.Create {
								// Set directory permissions to 777
								if err := os.Chmod(ev.Name, 0777); err != nil {
									logrus.Warnf("failed to set permissions for new directory %s: %v", ev.Name, err)
								}
								// 添加监听
								fw.watch.Add(ev.Name)
								logrus.Infof("new subdir created,start to watch it:%s", ev.Name)

								// 递归监听子目录中的所有子目录
								filepath.Walk(ev.Name, func(path string, info os.FileInfo, err error) error {
									if err != nil {
										return nil
									}
									if info.IsDir() && path != ev.Name {
										// Set directory permissions to 777
										if err := os.Chmod(path, 0777); err != nil {
											logrus.Warnf("failed to set permissions for subdir %s: %v", path, err)
										}
										if err := fw.watch.Add(path); err != nil {
											logrus.Errorf("failed to watch subdir %s: %v", path, err)
										} else {
											logrus.Infof("recursively watching subdir: %s", path)
										}
									}
									return nil
								})
							}
						} else {
							// Check if we've already processed this file
							if fw.processedFiles[ev.Name] {
								logrus.Debugf("file %s already processed, skipping", ev.Name)
								continue
							}
							_, err = isFileWriteComplete(ev.Name)
							if err != nil {
								logrus.Errorf("file write incomplete or error: %v", err)
							} else {
								logrus.Infof("capture a file:%s", ev.Name)
								// Mark as processed
								fw.processedFiles[ev.Name] = true
								// send file to receiver channel
								fw.receiver <- ev.Name
							}
						}
					}
				}

				if ev.Op&fsnotify.Remove == fsnotify.Remove {
					// if subdir removed
					// then remove the subdir's watch
					fi, err := os.Stat(ev.Name)
					if err == nil && fi.IsDir() {
						fw.watch.Remove(ev.Name)
						logrus.Infof("subdir is removed, no more to watch:%s", ev.Name)
					} else {
						// Clean up processed files map for removed files
						delete(fw.processedFiles, ev.Name)
					}
				}

				if ev.Op&fsnotify.Rename == fsnotify.Rename {
					// if subdir renamed
					// then the subdir's will be remove watch
					fw.watch.Remove(ev.Name)
					logrus.Infof("subdir is renamed, no more to watch:%s", ev.Name)
				}
			}
		case err := <-fw.watch.Errors:
			{
				logrus.Errorf("unexcepted watch error:%s", err)
				return
			}
		}
	}
}

func isFileWriteComplete(filePath string) (ok bool, err error) {
	for {
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			err = errors.Wrapf(err, "failed to get file info")
			ok = false
			return ok, err
		}

		// get file size
		initialSize := fileInfo.Size()

		time.Sleep(1 * time.Second)

		// get file size again
		fileInfo, err = os.Stat(filePath)
		if err != nil {
			err = errors.Wrapf(err, "failed to get file info again")
			ok = false
			return ok, err
		}

		// check filesize changed
		if fileInfo.Size() == initialSize {
			ok = true
			return ok, nil
		} else {
			continue
		}
	}
}
