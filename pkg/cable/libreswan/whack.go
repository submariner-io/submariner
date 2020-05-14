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
	async         bool
}

// Initiate initiates the named connection
func Initiate(name string) error {
	msg := whackMessage{
		name:          name,
		whackInitiate: true,
		async:         true,
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
		describeField("magic", uint32(1869114159), 0), // unsigned int

		describeField("whack_status", uint8(0), 4),             // bool
		describeField("whack_global_status", uint8(0), 5),      // bool
		describeField("whack_clear_stats", uint8(0), 6),        // bool
		describeField("whack_traffic_status", uint8(0), 7),     // bool
		describeField("whack_shunt_status", uint8(0), 8),       // bool
		describeField("whack_fips_status", uint8(0), 9),        // bool
		describeField("whack_brief_status", uint8(0), 10),      // bool
		describeField("whack_seccomp_crashtest", uint8(0), 11), // bool

		describeField("whack_shutdown", uint8(0), 12), // bool

		// End of basic commands

		describeField("name_len", uint64(0), 16), // size_t
		describeField("name", uint64(0), 24),     // chat *

		describeField("whack_options", uint8(0), 32), // bool

		// describeField("debugging", 16, 40), // lmod_t
		// describeField("impairing", 16, 56), // lmod_t

		// describeField("impairment", 8, 72), // struct whack_impair

		describeField("whack_connection", uint8(0), 80),          // bool
		describeField("whack_async", convertBool(msg.async), 81), // bool

		describeField("policy", uint64(0), 88),         // lset_t (default 0x04000400000006)
		describeField("sighash_policy", uint64(0), 96), // lset_t (default 0x07)
		// v3.29: deltatime_t contains an ms field, which is an intmax_t, 8 bytes
		// v3.30: deltatime_t contains a struct timeval dt, which contains time_t tv_sec and suseconds_t tv_usec
		// deltatime() initialises seconds only
		// deltatime_t; default deltatime(IKE_SA_LIFETIME_DEFAULT), i.e. 3600s
		describeField("sa_ike_life_seconds.dt.tv_sec", uint64(3600), 104),
		describeField("sa_ike_life_seconds.dt.tv_usec", uint64(0), 112),
		// deltatime_t; default deltatime(IPSEC_SA_LIFETIME_DEFAULT), i.e. 8 * 3600s
		describeField("sa_ipsec_life_seconds.dt.tv_sec", uint64(8*3600), 120),
		describeField("sa_ipsec_life_seconds.dt.tv_usec", uint64(0), 128),
		// deltatime_t; default deltatime(SA_REPLACEMENT_MARGIN_DEFAULT), i.e. 9 * 60s
		describeField("sa_rekey_margin.dt.tv_sec", uint64(9*60), 136),
		describeField("sa_rekey_margin.dt.tv_usec", uint64(0), 144),
		describeField("sa_rekey_fuzz", uint64(100), 152),      // unsigned long; default SA_REPLACEMENT_FUZZ_DEFAULT, 100
		describeField("sa_keying_tries", uint64(0), 160),      // unsigned long; default SA_REPLACEMENT_RETRIES_DEFAULT, 0
		describeField("sa_replay_window", uint64(32), 168),    // unsigned long; default IPSEC_SA_DEFAULT_REPLAY_WINDOW, 32
		describeField("r_timeout.dt.tv_sec", uint64(60), 176), // deltatime_t; default deltatime(RETRANSMIT_TIMEOUT_DEFAULT), i.e. 60s
		describeField("r_timeout.dt.tv_usec", uint64(0), 184),
		// deltatime_t; default deltatime_ms(RETRANSMIT_INTERVAL_DEFAULT_MS), i.e. 500ms
		describeField("r_interval.dt.tv_sec", uint64(0), 192),
		describeField("r_interval.dt.tv_usec", uint64(500*1000), 200),
		describeField("nic_offload", uint32(1), 208), // enum yna_options; default yna_auto, 1
		describeField("xfrm_if_id", uint32(0), 212),  // uint32_t

		describeField("dpd_delay.dt.tv_sec", uint64(0), 216),    // deltatime_t TODO
		describeField("dpd_delay.dt.tv_usec", uint64(0), 224),   // deltatime_t
		describeField("dpd_timeout.dt.tv_sec", uint64(0), 232),  // deltatime_t
		describeField("dpd_timeout.dt.tv_usec", uint64(0), 240), // deltatime_t
		describeField("dpd_action", uint32(0), 248),             // enum dpd_action
		describeField("dpd_count", uint32(0), 252),              // int

		describeField("remotepeertype", uint32(0), 256), // enum keyword_remotepeertype

		describeField("encaps", uint32(0), 260), // enum yna_options

		describeField("nat_keepalive", uint8(1), 264), // bool; default TRUE
		describeField("ikev1_natt", uint32(0), 268),   // enum ikev1_natt_policy

		describeField("initial_contact", uint8(0), 272), // bool

		describeField("cisco_unity", uint8(0), 273), // bool

		describeField("fake_strongswan", uint8(0), 274), // bool

		describeField("send_vendorid", uint8(0), 275), // bool

		describeField("nmconfigured", uint8(0), 276), // bool

		describeField("xauthby", uint32(0), 280), // enum keyword_xauthby; default XAUTHBY_FILE, 0

		describeField("xauthfail", uint32(0), 284), // enum keyword_xauthfail; default XAUTHFAIL_HARD, 0
		describeField("send_ca", uint32(0), 288),   // enum send_ca_policy

		describeField("connmtu", uint32(0), 292), // int

		describeField("sa_priority", uint32(0), 296),    // uint32_t
		describeField("sa_tfcpad", uint32(0), 300),      // uint32_t
		describeField("send_no_esp_tfc", uint8(0), 304), // bool
		describeField("sa_reqid", uint32(0), 308),       // reqid_t
		describeField("nflog_group", uint32(0), 312),    // int

		describeField("labeled_ipsec", uint8(0), 316), // bool
		describeField("policy_label", uint64(0), 320), // char *

		describeField("left.id", uint64(0), 328),     // char *
		describeField("left.pubkey", uint64(0), 336), // char *
		describeField("left.ca", uint64(0), 344),     // char *
		describeField("left.groups", uint64(0), 352), // char *

		describeField("left.authby", uint32(0), 360), // enum keyword_authby

		describeField("left.host_type", uint32(0), 364), // enum keyword_host
		// describeField("left.host_addr", 24, 368),        // ip_address
		// describeField("left.host_nexthop", 24, 392),     // ip_address
		// describeField("left.host_srcip", 24, 416),       // ip_address
		// describeField("left.client", 28, 440),           // ip_subnet
		// describeField("left.host_vtiip", 28, 468),       // ip_subnet
		// describeField("left.ifaceip", 28, 496),          // ip_subnet

		describeField("left.key_from_DNS_on_demand", uint8(0), 524), // bool
		describeField("left.pubkey_type", uint32(0), 528),           // enum whack_pubkey_type
		describeField("left.has_client", uint8(0), 532),             // bool
		describeField("left.has_client_wildcard", uint8(0), 533),    // bool
		describeField("left.has_port_wildcard", uint8(0), 534),      // bool
		describeField("left.updown", uint64(0), 536),                // char *
		describeField("left.host_port", uint16(0), 544),             // uint16_t
		describeField("left.port", uint16(0), 546),                  // uint16_t
		describeField("left.protocol", uint8(0), 548),               // uint8_t
		describeField("left.virt", uint64(0), 552),                  // char *
		// describeField("left.pool_range", 52, 560),                   // ip_range
		describeField("left.xauth_server", uint8(0), 612),    // bool
		describeField("left.xauth_client", uint8(0), 613),    // bool
		describeField("left.xauth_username", uint64(0), 616), // char *
		describeField("left.modecfg_server", uint8(0), 624),  // bool
		describeField("left.modecfg_client", uint8(0), 625),  // bool
		describeField("left.cat", uint8(0), 626),             // bool
		describeField("left.tundev", uint32(0), 628),         // unsigned int
		describeField("left.sendcert", uint32(0), 632),       // enum certpolicy
		describeField("left.send_ca", uint8(0), 636),         // bool
		describeField("left.certtype", uint32(0), 640),       // enum ike_cert_type

		describeField("left.host_addr_name", uint64(0), 648), // char *

		describeField("right.id", uint64(0), 656),     // char *
		describeField("right.pubkey", uint64(0), 664), // char *
		describeField("right.ca", uint64(0), 672),     // char *
		describeField("right.groups", uint64(0), 680), // char *

		describeField("right.authby", uint32(0), 688), // enum keyword_authby

		describeField("right.host_type", uint32(0), 692), // enum keyword_host
		// describeField("right.host_addr", 24, 696),        // ip_address
		// describeField("right.host_nexthop", 24, 720),     // ip_address
		// describeField("right.host_srcip", 24, 744),       // ip_address
		// describeField("right.client", 28, 768),           // ip_subnet
		// describeField("right.host_vtiip", 28, 796),       // ip_subnet
		// describeField("right.ifaceip", 28, 824),          // ip_subnet

		describeField("right.key_from_DNS_on_demand", uint8(0), 852), // bool
		describeField("right.pubkey_type", uint32(0), 856),           // enum whack_pubkey_type
		describeField("right.has_client", uint8(0), 860),             // bool
		describeField("right.has_client_wildcard", uint8(0), 861),    // bool
		describeField("right.has_port_wildcard", uint8(0), 862),      // bool
		describeField("right.updown", uint64(0), 864),                // char *
		describeField("right.host_port", uint16(500), 872),           // uint16_t; default IKE_UDP_PORT, 500
		describeField("right.port", uint16(0), 874),                  // uint16_t
		describeField("right.protocol", uint8(0), 876),               // uint8_t
		describeField("right.virt", uint64(0), 880),                  // char *
		// describeField("right.pool_range", 52, 888),                   // ip_range
		describeField("right.xauth_server", uint8(0), 940),    // bool
		describeField("right.xauth_client", uint8(0), 941),    // bool
		describeField("right.xauth_username", uint64(0), 944), // char *
		describeField("right.modecfg_server", uint8(0), 952),  // bool
		describeField("right.modecfg_client", uint8(0), 953),  // bool
		describeField("right.cat", uint8(0), 954),             // bool
		describeField("right.tundev", uint32(0), 956),         // unsigned int
		describeField("right.sendcert", uint32(0), 960),       // enum certpolicy
		describeField("right.send_ca", uint8(0), 964),         // bool
		describeField("right.certtype", uint32(0), 968),       // enum ike_cert_type

		describeField("right.host_addr_name", uint64(0), 976), // char *

		describeField("addr_family", uint16(2), 984),        // sa_family_t; default AF_INET, 2
		describeField("tunnel_addr_family", uint16(2), 986), // sa_family_t; default AF_INET, 2

		describeField("ike", uint64(0), 992),       // char *
		describeField("pfsgroup", uint64(0), 1000), // char *
		describeField("esp", uint64(0), 1008),      // char *

		describeField("whack_key", uint8(0), 1016),    // bool
		describeField("whack_addkey", uint8(0), 1017), // bool
		describeField("keyid", uint64(0), 1024),       // char *
		describeField("pubkey_alg", uint32(0), 1032),  // enum pubkey_alg
		// describeField("keyval", 16, 1040),             // chunk_t

		describeField("remote_host", uint64(0), 1056), // char *

		describeField("whack_route", convertBool(msg.whackRoute), 1064), // bool

		describeField("whack_unroute", convertBool(false), 1065), // bool

		describeField("whack_initiate", convertBool(msg.whackInitiate), 1066), // bool

		describeField("whack_oppo_initiate", uint8(0), 1067), // bool
		// ip_address contains an unsigned version, 16 bytes for the IP address, and 2 bytes for the hport
		describeField("oppo_my_client.version", uint32(0), 1068), // ip_address
		// describeField("oppo_my_client.bytes", 16, 1072),          // ip_address
		describeField("oppo_my_client.hport", uint16(0), 1088),     // ip_address
		describeField("oppo_peer_client.version", uint32(0), 1092), // ip_address
		// describeField("oppo_peer_client.bytes", 16, 1096),              // ip_address
		describeField("oppo_peer_client.hport", uint16(0), 1112), // ip_address
		describeField("oppo_proto", uint32(0), 1116),             // int
		describeField("oppo_dport", uint32(0), 1120),             // int

		describeField("whack_terminate", uint8(0), 1124), // bool

		describeField("whack_delete", uint8(0), 1125), // bool

		describeField("whack_deletestate", uint8(0), 1126),    // bool
		describeField("whack_deletestateno", uint64(0), 1128), // long unsigned int

		describeField("whack_rekey_ike", uint8(0), 1136),   // bool
		describeField("whack_rekey_ipsec", uint8(0), 1137), // bool

		describeField("whack_nfloggroup", uint64(0), 1144), // long unsigned int

		describeField("whack_deleteuser", uint8(0), 1152),      // bool
		describeField("whack_deleteuser_name", uint8(0), 1153), // bool

		describeField("whack_deleteid", uint8(0), 1154),      // bool
		describeField("whack_deleteid_name", uint8(0), 1155), // bool

		describeField("whack_purgeocsp", uint8(0), 1156), // bool

		describeField("whack_listen", uint8(0), 1157),        // bool
		describeField("whack_unlisten", uint8(0), 1158),      // bool
		describeField("ike_buf_size", uint64(0), 1160),       // long unsigned int
		describeField("ike_sock_err_toggle", uint8(0), 1168), // bool

		describeField("whack_ddos", uint32(0), 1172), // enum ddos_mode

		describeField("whack_ddns", uint8(0), 1176), // bool

		describeField("whack_crash", uint8(0), 1177), // bool
		// describeField("whack_crash_peer", 24, 1180),  // ip_address

		describeField("whack_utc", uint8(0), 1204),            // bool
		describeField("whack_check_pub_keys", uint8(0), 1205), // bool
		describeField("whack_list", uint64(0), 1208),          // lset_t

		describeField("whack_reread", uint8(0), 1216), // u_char

		describeField("connalias", uint64(0), 1224), // char *

		describeField("modecfg_dns", uint64(0), 1232),     // char *
		describeField("modecfg_domains", uint64(0), 1240), // char *
		describeField("modecfg_banner", uint64(0), 1248),  // char *

		describeField("conn_mark_both", uint64(0), 1256), // char *
		describeField("conn_mark_in", uint64(0), 1264),   // char *
		describeField("conn_mark_out", uint64(0), 1272),  // char *

		describeField("vti_iface", uint64(0), 1280),  // char *
		describeField("vti_routing", uint8(0), 1288), // bool
		describeField("vti_shared", uint8(0), 1289),  // bool

		describeField("redirect_to", uint64(0), 1296),        // char *
		describeField("accept_redirect_to", uint64(0), 1304), // char *

		describeField("active_redirect", uint8(0), 1312), // bool
		// describeField("active_redirect_peer", 24, 1316),  // ip_address
		// describeField("active_redirect_gw", 24, 1340),    // ip_address

		describeField("metric", uint32(0), 1364), // int

		describeField("dnshostname", uint64(0), 1368), // char *

		describeField("opt_set", uint32(0), 1376), // enum whack_opt_set
		describeField("string1", uint64(0), 1384), // char *
		describeField("string2", uint64(0), 1392), // char *
		describeField("string3", uint64(0), 1400), // char *

		describeField("str_size", uint64(0), 1408), // size_t

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
			return fmt.Errorf("Error building the packed buffer, at field %s: %v", v.name, err)
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

	connection, err := net.Dial("unix", "/run/pluto/pluto.ctl")
	if err != nil {
		return fmt.Errorf("error connecting to Pluto: %v", err)
	}
	defer connection.Close()
	_, err = buf.WriteTo(connection)
	if err != nil {
		return fmt.Errorf("error writing to Pluto: %v", err)
	}

	readBuf := make([]byte, 1024)

	for {
		n, err := connection.Read(readBuf)
		if err != nil {
			break
		}

		klog.Infof("Received %s\n", string(readBuf[0:n]))
	}

	return nil
}
