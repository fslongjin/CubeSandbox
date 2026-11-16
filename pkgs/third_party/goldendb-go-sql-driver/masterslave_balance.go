package mysql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
)

// ConnectMasterSlave implements masterslave load balancing similar to pymysql's _load_balance_masterslave.
// 1. Iterate through ip:port list, connect to each node
// 2. Check master_flag from handshake packet to determine if master
// 3. If slave, query select @@global.cluster_master_host to get master address (ip:port format)
// 4. Insert master address at front of list for priority connection
// 5. Continue until master is found or all nodes fail
func (c *connector) ConnectMasterSlave(ctx context.Context) (driver.Conn, error) {
	// Copy address list to avoid modifying original config
	hosts := make([]string, len(c.cfg.Addrs))
	copy(hosts, c.cfg.Addrs)

	var lastErr error

	for len(hosts) > 0 {
		// Pop element from head of list
		element := hosts[0]
		hosts = hosts[1:]

		// Connect
		mc, err := c.doConnect(ctx, element)
		if err != nil {
			lastErr = fmt.Errorf("failed to connect to %s: %w", element, err)
			fmt.Printf("Failed to establish connections to host: %s. Error: %v\n", element, err)
			continue
		}

		// Check if master node via master_flag in handshake
		if mc.(*mysqlConn).isMaster {
			return mc, nil
		}

		// Slave node, query master address
		masterAddr, queryErr := c.queryMasterHost(mc.(*mysqlConn))
		if queryErr != nil {
			fmt.Printf("Query master host failed from %s: %v\n", element, queryErr)
		} else if masterAddr != "" {
			// Exact match dedup by ip:port: multiple DB instances can run on same machine with different ports
			newHosts := make([]string, 0, len(hosts))
			for _, h := range hosts {
				if h != masterAddr {
					newHosts = append(newHosts, h)
				}
			}
			hosts = newHosts

			// Insert master at front of list to ensure it's used in next iteration
			hosts = append([]string{masterAddr}, hosts...)
			fmt.Printf("Found master node: %s, added to front of list\n", masterAddr)
		}

		// Close current connection
		mc.Close()
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("failed to connect to all IP:port pairs")
}

// queryMasterHost queries master address from a connected slave node
func (c *connector) queryMasterHost(mc *mysqlConn) (string, error) {
	handleOk := mc.clearResult()

	if err := mc.writeCommandPacketStr(comQuery, "select @@global.cluster_master_host"); err != nil {
		return "", err
	}

	resLen, err := handleOk.readResultSetHeaderPacket()
	if err != nil {
		return "", err
	}

	if resLen == 0 {
		return "", errors.New("empty result set")
	}

	// Read column info
	if err := mc.readUntilEOF(); err != nil {
		return "", err
	}

	// Read data row
	rows := new(textRows)
	rows.mc = mc
	rows.rs.columns = []mysqlField{{fieldType: fieldTypeVarChar}}

	dest := make([]driver.Value, 1)
	if err := rows.readRow(dest); err != nil {
		return "", err
	}

	// Read EOF marker
	if err := mc.readUntilEOF(); err != nil {
		return "", err
	}

	if dest[0] == nil {
		return "", nil
	}

	masterAddr := strings.TrimSpace(string(dest[0].([]byte)))
	return masterAddr, nil
}
