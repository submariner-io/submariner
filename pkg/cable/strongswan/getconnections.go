/*
© 2021 Red Hat, Inc. and others

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
package strongswan

import (
	"github.com/bronze1man/goStrongswanVici"
	"github.com/pkg/errors"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (i *strongSwan) GetConnections() (*[]v1.Connection, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()

	sas, err := client.ListSas("", "")
	if err != nil {
		return nil, errors.WithMessage(err, "Error while retrieving active IKE_SAs")
	}

	return i.getSAListConnections(sas)
}

// getSAListConnections converts an IKE SA List from ListSas, and converts into a cable connection list
// It's made a separate function for testability (unit tests)
func (i *strongSwan) getSAListConnections(sas []map[string]goStrongswanVici.IkeSa) (*[]v1.Connection, error) {
	connections := []v1.Connection{}

	for cableID, endpoint := range i.remoteEndpoints {
		connection := v1.NewConnection(endpoint)
		found := false
		for _, saMap := range sas {
			if sa, ok := saMap[cableID]; ok {
				found = true

				updateConnectionState(&sa, connection)

				break
			}
		}

		if !found {
			connection.SetStatus(v1.ConnectionError, "No IKE SA found for cable %s", cableID)
		}

		connections = append(connections, *connection)
	}

	return &connections, nil
}

// updateConnectionState reads the IKE SA connection state and translates that into the cable
// connection status, IKE SA states for strongswan can be found here:
//  https://github.com/strongswan/strongswan/blob/d30498edf1ebda4c6f49dab8192a632b871cc7da/src/libcharon/sa/ike_sa.h#L304
//
func updateConnectionState(sa *goStrongswanVici.IkeSa, connection *v1.Connection) {
	switch sa.State {
	case "CREATED":
		connection.SetStatus(v1.ConnectionError, "Connection has been created but not yet started")

	case "CONNECTING":
		connection.SetStatus(v1.Connecting,
			"Connecting to %s:%s", sa.Remote_host, sa.Remote_port)

	case "ESTABLISHED":
		connection.SetStatus(v1.Connected,
			"Connected to %s:%s - encryption alg=%s, keysize=%s rekey-time=%s",
			sa.Remote_host, sa.Remote_port, sa.Encr_alg, sa.Encr_keysize, sa.Rekey_time)

	case "REKEYING":
		connection.SetStatus(v1.Connected, "The connection is being re-keyed")

	case "REKEYED":
		connection.SetStatus(v1.Connected,
			"Connection has been re-keyed, encryption alg=%s, keysize %s rekey-time=%s",
			sa.Encr_alg, sa.Encr_keysize, sa.Rekey_time)

	case "DELETING":
		connection.SetStatus(v1.ConnectionError, "The connection is being deleted")

	case "DESTROYING":
		connection.SetStatus(v1.ConnectionError, "The connection is being destroyed")

	case "PASSIVE":
		connection.SetStatus(v1.ConnectionError, "Connection is in a passive state")

	default:
		connection.SetStatus(v1.ConnectionError, "Unexpected IKE SA state: %s", sa.State)
	}
}
