package hostdb

import (
	"bytes"
	"time"

	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
)

// A hostEntry represents a host on the network.
type hostEntry struct {
	modules.HostDBEntry

	FirstSeen   types.BlockHeight
	Weight      types.Currency
	Reliability types.Currency
	LastScanned time.Time
	LastSeen    time.Time
}

// insertHost adds a host entry to the state. The host will be inserted into
// the set of all hosts, and if it is online and responding to requests it will
// be put into the list of active hosts.
//
// TODO: Function should return an error.
func (hdb *HostDB) insertHost(host modules.HostDBEntry) {
	// Remove garbage hosts and local hosts (but allow local hosts in testing).
	if err := host.NetAddress.IsValid(); err != nil {
		hdb.log.Debugf("WARN: host '%v' has an invalid NetAddress: %v", host.NetAddress, err)
		return
	}
	// Don't do anything if we've already seen this host and the public key is
	// the same.
	if knownHost, exists := hdb.allHosts[host.NetAddress]; exists && bytes.Equal(host.PublicKey.Key, knownHost.PublicKey.Key) {
		return
	}

	// Create hostEntry and add to allHosts.
	h := &hostEntry{
		FirstSeen:   hdb.blockHeight,
		HostDBEntry: host,
		Reliability: DefaultReliability,
	}
	hdb.allHosts[host.NetAddress] = h

	// Add the host to the scan queue. If the scan is successful, the host
	// will be placed in activeHosts.
	hdb.queueHostEntry(h)
}

// Remove deletes an entry from the hostdb.
func (hdb *HostDB) removeHost(addr modules.NetAddress) error {
	// See if the node is in the set of active hosts.
	node, exists := hdb.activeHosts[addr]
	if exists {
		node.removeNode()
		delete(hdb.activeHosts, addr)
	}

	// Remove the node from all hosts.
	delete(hdb.allHosts, addr)

	return nil
}

// Host returns the HostSettings associated with the specified NetAddress. If
// no matching host is found, Host returns false.
func (hdb *HostDB) Host(addr modules.NetAddress) (modules.HostDBEntry, bool) {
	hdb.mu.Lock()
	defer hdb.mu.Unlock()
	entry, ok := hdb.allHosts[addr]
	if !ok || entry == nil {
		return modules.HostDBEntry{}, false
	}
	return entry.HostDBEntry, true
}

// ActiveHosts returns the hosts that can be randomly selected out of the
// hostdb, sorted by preference.
func (hdb *HostDB) ActiveHosts() (activeHosts []modules.HostDBEntry) {
	hdb.mu.RLock()
	numHosts := len(hdb.activeHosts)
	hdb.mu.RUnlock()

	// Get the hosts using RandomHosts so that they are in sorted order.
	sortedHosts := hdb.RandomHosts(numHosts, nil)
	return sortedHosts
}

// AllHosts returns all of the hosts known to the hostdb, including the
// inactive ones.
func (hdb *HostDB) AllHosts() (allHosts []modules.HostDBEntry) {
	hdb.mu.RLock()
	defer hdb.mu.RUnlock()

	for _, entry := range hdb.allHosts {
		allHosts = append(allHosts, entry.HostDBEntry)
	}
	return
}

// AverageContractPrice returns the average price of a host.
func (hdb *HostDB) AverageContractPrice() types.Currency {
	// maybe a more sophisticated way of doing this
	var totalPrice types.Currency
	sampleSize := 18
	hosts := hdb.RandomHosts(sampleSize, nil)
	if len(hosts) == 0 {
		return totalPrice
	}
	for _, host := range hosts {
		totalPrice = totalPrice.Add(host.ContractPrice)
	}
	return totalPrice.Div64(uint64(len(hosts)))
}

// IsOffline reports whether h is offline. A host is considered offline if it
// has been scanned at least once in the last three days, but hasn't responded
// to the scan during that period. If the host has not been scanned in the last
// three days, IsOffline will scan it, so the caller should treat this call as
// blocking. If the host is not present in the HostDB, IsOffline returns false.
func (hdb *HostDB) IsOffline(addr modules.NetAddress) bool {
	// lookup entry
	hdb.mu.RLock()
	var lastSeen, lastScanned time.Time
	entry, ok := hdb.allHosts[addr]
	if ok {
		lastSeen, lastScanned = entry.LastSeen, entry.LastScanned
	}
	hdb.mu.RUnlock()
	if !ok {
		return false
	}

	if time.Since(lastScanned) > uptimeThreshold {
		// if entry hasn't been scanned in the last 3 days, scan it now
		hdb.managedScanHost(entry)

		// update lastSeen
		hdb.mu.RLock()
		entry, ok := hdb.allHosts[addr]
		if ok {
			lastSeen = entry.LastSeen
		}
		hdb.mu.RUnlock()
		if !ok {
			// should (almost) never happen
			hdb.log.Printf("WARN: no entry in allHosts for %v immediately after scanning", addr)
			return false
		}
	}

	// at this point we know the host has been scanned at least once within the
	// last 3 days, so return whether it has been seen during that time.
	return time.Since(lastSeen) > uptimeThreshold
}
