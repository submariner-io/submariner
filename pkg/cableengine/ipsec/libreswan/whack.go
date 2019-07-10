package libreswan

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"

	"k8s.io/klog"
)

type whackMessage struct {
	name          string
	whackInitiate bool
	whackRoute    bool
}

// Initiate initiates the named connection
func Initiate(name string) error {
	msg := whackMessage{
		name:          name,
		whackInitiate: true,
	}

	return whack(msg)
}

// Route sets up routing for the named connection
func Route(name string) error {
	msg := whackMessage{
		name:       name,
		whackRoute: true,
	}

	return whack(msg)
}

type fieldDesc struct {
	name   string
	data   interface{}
	offset int
}

func describeField(name string, data interface{}, offset int) fieldDesc {
	return fieldDesc{
		name:   name,
		data:   data,
		offset: offset,
	}
}

func convertBool(value bool) uint8 {
	if value {
		return uint8(1)
	}
	return uint8(0)
}

func whack(msg whackMessage) error {
	// TODO port the validation

	buf := new(bytes.Buffer)
	var data = []fieldDesc{
		describeField("magic", uint32(1869114158), 0),          // unsigned int
		describeField("whack_status", uint8(0), 4),             // bool
		describeField("whack_global_status", uint8(0), 5),      // bool
		describeField("whack_clear_stats", uint8(0), 6),        // bool
		describeField("whack_traffic_status", uint8(0), 7),     // bool
		describeField("whack_shunt_status", uint8(0), 8),       // bool
		describeField("whack_fips_status", uint8(0), 9),        // bool
		describeField("whack_brief_status", uint8(0), 10),      // bool
		describeField("whack_seccomp_crashtest", uint8(0), 11), // bool

		describeField("whack_shutdown", uint8(0), 12), // bool

		describeField("name_len", uint64(0), 16), // size_t
		describeField("name", uint64(0), 24),     // chat *

		describeField("whack_options", uint8(0), 32), // bool

		// lmod_t debugging (19, 40)
		// lmod_t impairing (21, 56)

		// struct whack_impair impairment (23, 72)

		describeField("whack_connection", uint8(0), 80), // bool
		describeField("whack_async", uint8(0), 81),      // bool

		// lset_t policy (28, 88)
		// lset_t sighash_policy
		// v3.29: deltatime_t contains an ms field, which is an intmax_t, 8 bytes
		// v3.30: deltatime_t contains a struct timeval dt, which contains time_t tv_sec and suseconds_t tv_usec
		// deltatime() initialises seconds only
		describeField("sa_ike_life_seconds.ms", uint64(3600*1000), 104), // deltatime_t; default deltatime(IKE_SA_LIFETIME_DEFAULT), i.e. 3600s
		/* v3.30
		describeField("sa_ike_life_seconds.dt.tv_sec", uint64(3600), 104), // deltatime_t; default deltatime(IKE_SA_LIFETIME_DEFAULT), i.e. 3600s
		describeField("sa_ike_life_seconds.dt.tv_usec", uint64(0), 112),
		*/
		describeField("sa_ipsec_life_seconds.ms", uint64(8*3600*1000), 112), // deltatime_t; default deltatime(IPSEC_SA_LIFETIME_DEFAULT), i.e. 8 * 3600s
		/* v3.30
		describeField("sa_ipsec_life_seconds.dt.tv_sec", uint64(8*3600), 120), // deltatime_t; default deltatime(IPSEC_SA_LIFETIME_DEFAULT), i.e. 8 * 3600s
		describeField("sa_ipsec_life_seconds.dt.tv_usec", uint64(0), 128),
		*/
		describeField("sa_rekey_margin.ms", uint64(9*60*1000), 120), // deltatime_t; default deltatime(SA_REPLACEMENT_MARGIN_DEFAULT), i.e. 9 * 60s
		/* v3.30
		describeField("sa_rekey_margin.dt.tv_sec", uint64(9*60), 136), // deltatime_t; default deltatime(SA_REPLACEMENT_MARGIN_DEFAULT), i.e. 9 * 60s
		describeField("sa_rekey_margin.dt.tv_usec", uint64(0), 144),
		*/
		/* v3.30 offsets are +24 from here */
		describeField("sa_rekey_fuzz", uint64(100), 128),    // unsigned long; default SA_REPLACEMENT_FUZZ_DEFAULT, 100
		describeField("sa_keying_tries", uint64(0), 136),    // unsigned long; default SA_REPLACEMENT_RETRIES_DEFAULT, 0
		describeField("sa_replay_window", uint64(32), 144),  // unsigned long; default IPSEC_SA_DEFAULT_REPLAY_WINDOW, 32
		describeField("r_timeout.ms", uint64(60*1000), 152), // deltatime_t; default deltatime(RETRANSMIT_TIMEOUT_DEFAULT), i.e. 60s
		/* v3.30
		describeField("r_timeout.dt.tv_sec", uint64(60), 176), // deltatime_t; default deltatime(RETRANSMIT_TIMEOUT_DEFAULT), i.e. 60s
		describeField("r_timeout.dt.tv_usec", uint64(0), 184),
		*/
		describeField("r_interval.ms", uint64(500), 160), // deltatime_t; default deltatime_ms(RETRANSMIT_INTERVAL_DEFAULT_MS), i.e. 500ms
		/* v3.30
		describeField("r_interval.dt.tv_sec", uint64(0), 192), // deltatime_t; default deltatime_ms(RETRANSMIT_INTERVAL_DEFAULT_MS), i.e. 500ms
		describeField("r_interval.dt.tv_usec", uint64(500*1000), 200),
		*/
		/* v3.30 offsets are +40 from here */
		describeField("nic_offload", uint32(1), 168), // enum yna_options; default yna_auto, 1

		// deltatime_t dpd_delay (45, 216)
		// deltatime_t dpd_timeout
		/* v3.30 offsets are +56 from here */
		describeField("dpd_action", uint32(0), 192), // enum dpd_action
		describeField("dpd_count", int32(0), 196),   // int

		describeField("remotepeertype", uint32(0), 200), // enum keyword_remotepeertype; default NON_CISCO, 0

		describeField("encaps", uint32(0), 204), // enum yna_options

		describeField("nat_keepalive", uint8(1), 208), // bool; default TRUE
		describeField("ikev1_natt", uint32(0), 212),   // enum ikev1_natt_policy

		describeField("initial_contact", uint8(0), 216), // bool

		describeField("cisco_unity", uint8(0), 217), // bool

		describeField("fake_strongswan", uint8(0), 218), // bool

		describeField("send_vendorid", uint8(0), 219), // bool

		describeField("nmconfigured", uint8(0), 220), // bool

		describeField("xauthby", uint32(0), 224), // enum keyword_xauthby; default XAUTHBY_FILE, 0

		describeField("xauthfail", uint32(0), 228), // enum keyword_xauthfail; default XAUTHFAIL_HARD, 0
		describeField("send_ca", uint32(0), 232),   // enum send_ca_policy

		/*
			int32(0), // int connmtu

			uint32(0),           // uint32_t sa_priority
			uint32(0),           // uint32_t sa_tfcpad
			uint8(0),            // bool send_no_esp_tfc (70, 304)
			uint16(0), uint8(0), // padding
			uint32(0), // reqid_t sa_reqid (73, 308)
			int32(0),  // int nflog_group

			uint8(0),            // bool labeled_ipsec (75, 316)
			uint16(0), uint8(0), // padding
			uint64(0), // char *policy_label (78, 320)

			// struct whack_end left
			uint64(0), // char *id
			uint64(0), // char *pubkey
			uint64(0), // char *ca
			uint64(0), // char *groups

			uint32(0), // enum keyword_authby authby (83, 360)

			uint32(0), // enum keyword_host host_type
			// ip_address is either sockaddr_in or sockaddr_in6; sockaddr_in6 takes 28 bytes
			// ip_address host_addr
			uint16(0),            // sa_family_t sin6_family (85, 368)
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo (87, 372)
			uint64(0), uint64(0), // struct in6_addr sin6_addr (88, 376)
			uint32(0), // uint32_t sin6_scope_id
			// ip_address host_nexthop
			uint16(0),            // sa_family_t sin6_family (91, 396)
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo (93, 400)
			uint64(0), uint64(0), // struct in6_addr sin6_addr (94, 404)
			uint32(0), // uint32_t sin6_scope_id
			// ip_address host_srcip
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			// ip_subnet is ip_address + int mask
			// ip_subnet client
			uint16(0),            // sa_family_t sin6_family (103, 452)
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			uint32(0), // int maskbits (109, 480)
			// ip_subnet host_vtiip
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			uint32(0), // int maskbits

			uint8(0),            // bool key_from_DNS_on_demand (117, 516)
			uint16(0), uint8(0), // padding
			uint32(0),           // enum whack_pubkey_type pubkey_type (120, 520)
			uint8(0),            // bool has_client
			uint8(0),            // bool has_client_wildcard
			uint8(0),            // bool has_port_wildcard
			uint8(0),            // padding
			uint64(0),           // char *updown (125, 528)
			uint16(0),           // uint16_t host_port
			uint16(0),           // uint16_t port
			uint8(0),            // uint8_t protocol
			uint16(0), uint8(0), // padding
			uint64(0), // char *virt (131, 544)
			// ip_range is ip_address start + end
			// ip_range pool_range
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0),            // uint32_t sin6_scope_id
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0),            // uint32_t sin6_scope_id
			uint8(0),             // bool xauth_server (144, 608)
			uint8(0),             // bool xauth_client
			uint32(0), uint16(0), // padding
			uint64(0),           // char *xauth_username (148, 616)
			uint8(0),            // bool modecfg_server
			uint8(0),            // bool modecfg_client
			uint8(0),            // bool cat
			uint8(0),            // padding
			uint32(0),           // unsigned int tundev (153, 628)
			uint32(0),           // enum certpolicy sendcert
			uint8(0),            // bool send_ca
			uint16(0), uint8(0), // padding
			uint32(0), // enum ike_cert_type certtype (158, 640)

			uint32(0), // padding

			uint64(0), // char *host_addr_name (160, 648)

			// struct whack_end right
			uint64(0), // char *id
			uint64(0), // char *pubkey
			uint64(0), // char *ca
			uint64(0), // char *groups

			uint32(0), // enum keyword_authby authby

			uint32(0), // enum keyword_host host_type
			// ip_address is either sockaddr_in or sockaddr_in6; sockaddr_in6 takes 28 bytes
			// ip_address host_addr
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			// ip_address host_nexthop
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			// ip_address host_srcip
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			// ip_subnet is ip_address + int mask
			// ip_subnet client
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			uint32(0), // int maskbits
			// ip_subnet host_vtiip
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			uint32(0), // int maskbits

			uint8(0),  // bool key_from_DNS_on_demand
			uint32(0), // enum whack_pubkey_type pubkey_type
			uint8(0),  // bool has_client
			uint8(0),  // bool has_client_wildcard
			uint8(0),  // bool has_port_wildcard
			uint64(0), // char *updown
		*/
		describeField("right.host_port", uint16(500), 808), // uint16_t; default IKE_UDP_PORT, 500
		/*
			uint16(0), // uint16_t port
			uint8(0),  // uint8_t protocol
			uint64(0), // char *virt
			// ip_range pool_range
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0),            // uint32_t sin6_scope_id
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			uint8(0),  // bool xauth_server
			uint8(0),  // bool xauth_client
			uint64(0), // char *xauth_username
			uint8(0),  // bool modecfg_server
			uint8(0),  // bool modecfg_client
			uint8(0),  // bool cat
			uint32(0), // unsigned int tundev
			uint32(0), // enum certpolicy sendcert
			uint8(0),  // bool send_ca
			uint32(0), // enum ike_cert_type certtype

			uint64(0), // char *host_addr_name
		*/

		//describeField("addr_family", uint16(2), 984),        // sa_family_t; default AF_INET, 2 (ends up at 928 in tests)
		//describeField("tunnel_addr_family", uint16(2), 986), // sa_family_t; default AF_INET, 2
		describeField("addr_family", uint16(2), 928),        // sa_family_t; default AF_INET, 2 (ends up at 928 in tests)
		describeField("tunnel_addr_family", uint16(2), 930), // sa_family_t; default AF_INET, 2

		/*
			uint64(0), // char *ike
			uint64(0), // char *pfsgroup
			uint64(0), // char *esp

			uint8(0),             // bool whack_key
			uint8(0),             // bool whack_addkey
			uint64(0),            // char *keyid
			uint32(0),            // enum pubkey_alg pubkey_alg
			uint64(0), uint64(0), // chunk_t keyval

			uint64(0), // char *remote_host
		*/

		describeField("whack_route", convertBool(msg.whackRoute), 1008),       // bool
		describeField("whack_unroute", convertBool(false), 1009),              // bool
		describeField("whack_initiate", convertBool(msg.whackInitiate), 1010), // bool

		/*
			uint8(0), // bool whack_oppo_initiate
			// ip_address oppo_my_client
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			// ip_address oppo_peer_client
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			int32(0),  // int oppo_proto
			int32(0),  // int oppo_dport

			uint8(0), // bool whack_terminate

			uint8(0), // bool whack_delete

			uint8(0),  // bool whack_deletestate
			uint64(0), // long unsigned int whack_deletestateno

			uint64(0), // long unsigned int whack_nfloggroup

			uint8(0), // bool whack_deleteuser
			uint8(0), // bool whack_deleteuser_name

			uint8(0), // bool whack_deleteid
			uint8(0), // bool whack_deleteid_name

			uint8(0), // bool whack_purgeocsp

			uint8(0),  // bool whack_listen
			uint8(0),  // bool whack_unlisten
			uint64(0), // long unsigned int ike_buf_size
			uint8(0),  // bool ike_sock_err_toggle

			uint32(0), // enum ddos_mode whack_ddos

			uint8(0), // bool whack_ddns

			uint8(0), // bool whack_crash
			// ip_address whack_crash_peer
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id

			uint8(0),  // bool whack_utc
			uint8(0),  // bool whack_check_pub_keys
			uint64(0), // lset_t whack_list

			uint8(0), // u_char whack_reread

			uint64(0), // char *connalias

			uint64(0), // char *modecfg_dns
			uint64(0), // char *modecfg_domains
			uint64(0), // char *modecfg_banner

			uint64(0), // char *conn_mark_both
			uint64(0), // char *conn_mark_in
			uint64(0), // char *conn_mark_out

			uint64(0), // char *vti_iface
			uint8(0),  // bool vti_routing
			uint8(0),  // bool vti_shared

			uint64(0), // char *redirect_to
			uint64(0), // char *accept_redirect_to

			uint8(0), // bool active_redirect
			// ip_address active_redirect_peer
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id
			// ip_address active_redirect_gw
			uint16(0),            // sa_family_t sin6_family
			uint16(0),            // in_port_t sin6_port
			uint32(0),            // uint32_t sin6_flowinfo
			uint64(0), uint64(0), // struct in6_addr sin6_addr
			uint32(0), // uint32_t sin6_scope_id

			int32(0), // int metric

			uint64(0), // char *dnshostname

			uint32(0), // enum whack_opt_set opt_set
			uint64(0), // char *string1
			uint64(0), // char *string2
			uint64(0), // char *string3
		*/

		/* space for strings (hope there is enough room):
		 * Note that pointers don't travel on wire.
		 *  1 connection name [name_len]
		 *  2 left's name [left.host.name.len]
		 *  3 left's cert
		 *  4 left's ca
		 *  5 left's groups
		 *  6 left's updown
		 *  7 left's virt
		 *  8 right's name [left.host.name.len]
		 *  9 right's cert
		 * 10 right's ca
		 * 11 right's groups
		 * 12 right's updown
		 * 13 right's virt
		 * 14 keyid
		 * 15 unused (was myid)
		 * 16 ike
		 * 17 esp
		 * 18 left.xauth_username
		 * 19 right.xauth_username
		 * 20 connalias
		 * 21 left.host_addr_name
		 * 22 right.host_addr_name
		 * 23 genstring1  - used with opt_set
		 * 24 genstring2
		 * 25 genstring3
		 * 26 dnshostname
		 * 27 policy_label if compiled with with LABELED_IPSEC
		 * 28 remote_host
		 * 29 redirect_to
		 * 30 accept_redirect_to
		 * plus keyval (limit: 8K bits + overhead), a chunk.
		 */
		//describeField("str_size", uint64(0), 1424), // size_t
		describeField("str_size", uint64(0), 1368), // size_t

		// C strings tacked on one after the other, including the terminating nulls
		// Because of the string allocations, this needs to be handled in code, see below
		// Up to 4096 bytes
		// unsigned char string[4096]
	}
	var strings = []string{
		msg.name,        // name
		"",              // left.id
		"",              // left.pubkey
		"",              // left.ca
		"",              // left.groups
		"ipsec _updown", // left.updown (default value for now)
		"",              // left.virt
		"",              // right.id
		"",              // right.pubkey
		"",              // right.ca
		"",              // right.groups
		"ipsec _updown", // right.updown (default value for now)
		"",              // right.virt
		"",              // keyid
		"",              // ike
		"",              // esp
		"",              // left.xauth_username
		"",              // right.xauth_username
		"",              // connalias
		"",              // left.host_addr_name
		"",              // right.host_addr_name
		"",              // string1
		"",              // string2
		"",              // string3
		"",              // dnshostname
		"",              // policy_label
		"",              // modecfg_dns
		"",              // modecfg_domains
		"",              // modecfg_banner
		"",              // conn_mark_both
		"",              // conn_mark_in
		"",              // conn_mark_out
		"",              // vti_iface
		"",              // remote_host
		"",              // redirect_to
		"",              // accept_redirect_to
	}

	for _, v := range data {
		klog.V(4).Infof("Field %s at desired offset %d, current offset %d\n", v.name, v.offset, buf.Len())
		for buf.Len() < v.offset {
			if err := buf.WriteByte(0); err != nil {
				return fmt.Errorf("Error building the packed buffer: %v", err)
			}
		}
		if err := binary.Write(buf, binary.LittleEndian, v.data); err != nil {
			return fmt.Errorf("Error building the packed buffer: %v", err)
		}
	}
	bufLenPreStrings := buf.Len()
	for _, v := range strings {
		if err := binary.Write(buf, binary.LittleEndian, []byte(v)); err != nil {
			return fmt.Errorf("Error building the packed buffer: %v", err)
		}
		if err := binary.Write(buf, binary.LittleEndian, byte(0)); err != nil {
			return fmt.Errorf("Error building the packed buffer: %v", err)
		}
	}
	stringLength := buf.Len() - bufLenPreStrings
	if stringLength > 4096 {
		return fmt.Errorf("Strings too long, %d written instead of 4096", stringLength)
	}

	//buf.WriteTo(os.Stderr)

	connection, err := net.Dial("unix", "/run/pluto/pluto.ctl")
	if err != nil {
		return fmt.Errorf("Error connecting to Pluto: %v", err)
	}
	defer connection.Close()
	_, err = buf.WriteTo(connection)
	if err != nil {
		return fmt.Errorf("Error writing to Pluto: %v", err)
	}
	readBuf := make([]byte, 1024)
	for {
		n, err := connection.Read(readBuf[:])
		if err != nil {
			break
		}
		klog.V(4).Infof("Received %s\n", string(readBuf[0:n]))
	}

	return nil
}
