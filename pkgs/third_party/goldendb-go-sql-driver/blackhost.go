// Go MySQL Driver - A MySQL-Driver for Go's database/sql package

package mysql

import (
	"sync"
	"time"
)

var (
	globalBlacklistLock sync.RWMutex
	// Bad CN nodes
	globalBlacklist map[string]int64
)

// Get current timestamp in milliseconds
func nowTime() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// Add bad CN to blacklist map
func addToGlobalBlacklist(addr string, blackList int64) {
	globalBlacklistLock.Lock()
	defer globalBlacklistLock.Unlock()

	timeout := nowTime() + blackList
	if globalBlacklist == nil {
		globalBlacklist = make(map[string]int64)
	}
	globalBlacklist[addr] = timeout
}

// Return all bad CN nodes
func getGlobalBlacklist() map[string]int64 {
	var blacklistClone map[string]int64
	globalBlacklistLock.Lock()
	defer globalBlacklistLock.Unlock()

	// Deep copy the map
	blacklistClone = cloneMap(globalBlacklist)

	// Remove expired CN entries
	for key, value := range blacklistClone {
		if value < nowTime() {
			delete(blacklistClone, key)
			delete(globalBlacklist, key)
		}
	}

	return blacklistClone

}

func cloneMap(tags map[string]int64) map[string]int64 {
	cloneTags := make(map[string]int64)
	for k, v := range tags {
		cloneTags[k] = v
	}
	return cloneTags
}

// Filter out blacklisted CN nodes
func filterBlackHost(addrs []string) []string {

	filterBlackList := make([]string, len(addrs))
	copy(filterBlackList, addrs)

	blackListMap := getGlobalBlacklist()
	globalBlacklistLock.Lock()
	defer globalBlacklistLock.Unlock()

	for key := range blackListMap {
		if contains(addrs, key) {
			filterBlackList = removeElement(filterBlackList, key)
		}
	}
	return filterBlackList
}

// Remove specified element from []string
func removeElement(arr []string, elem string) []string {
	var result []string
	for _, val := range arr {
		if val != elem {
			result = append(result, val)
		}
	}
	return result
}

// Check if element v exists in string slice
func contains(elems []string, v string) bool {
	for _, s := range elems {
		if v == s {
			return true
		}
	}
	return false
}
