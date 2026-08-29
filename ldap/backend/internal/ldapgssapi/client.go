package ldapgssapi

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	krbclient "github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/gssapi"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/keytab"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/spnego"
	"github.com/jcmturner/gokrb5/v8/types"
)

// ClientOptions controls how the Kerberos GSSAPI context is negotiated.
// It intentionally defaults to AD Studio's baseline "authentication only"
// posture instead of forcing mutual/confidentiality on every bind.
type ClientOptions struct {
	UseIntegrity       bool
	UseConfidentiality bool
	MutualAuth         bool
}

func (o ClientOptions) normalized() ClientOptions {
	if !o.UseIntegrity && !o.UseConfidentiality {
		o.UseIntegrity = true
	}
	return o
}

func (o ClientOptions) contextFlags() []int {
	o = o.normalized()
	flags := make([]int, 0, 3)
	if o.UseIntegrity {
		flags = append(flags, gssapi.ContextFlagInteg)
	}
	if o.UseConfidentiality {
		flags = append(flags, gssapi.ContextFlagConf)
	}
	if o.MutualAuth {
		flags = append(flags, gssapi.ContextFlagMutual)
	}
	return flags
}

func (o ClientOptions) needInitContinuation() bool {
	o = o.normalized()
	return o.MutualAuth
}

// Client implements ldap.GSSAPIClient using gokrb5 with configurable
// context flags. The implementation is derived from go-ldap's upstream
// gssapi client so McpCTL can tune SASL/GSSAPI interop for AD.
type Client struct {
	*krbclient.Client

	ekey          types.EncryptionKey
	Subkey        types.EncryptionKey
	clientOptions ClientOptions
}

func NewClientWithKeytab(username, realm, keytabPath, krb5confPath string, opts ClientOptions, settings ...func(*krbclient.Settings)) (*Client, error) {
	krb5conf, err := config.Load(krb5confPath)
	if err != nil {
		return nil, err
	}

	kt, err := keytab.Load(keytabPath)
	if err != nil {
		return nil, err
	}

	client := krbclient.NewWithKeytab(username, realm, kt, krb5conf, settings...)
	return &Client{
		Client:        client,
		clientOptions: opts.normalized(),
	}, nil
}

func NewClientWithPassword(username, realm, password string, krb5confPath string, opts ClientOptions, settings ...func(*krbclient.Settings)) (*Client, error) {
	krb5conf, err := config.Load(krb5confPath)
	if err != nil {
		return nil, err
	}

	client := krbclient.NewWithPassword(username, realm, password, krb5conf, settings...)
	return &Client{
		Client:        client,
		clientOptions: opts.normalized(),
	}, nil
}

func NewClientFromCCache(ccachePath, krb5confPath string, opts ClientOptions, settings ...func(*krbclient.Settings)) (*Client, error) {
	krb5conf, err := config.Load(krb5confPath)
	if err != nil {
		return nil, err
	}

	ccache, err := credentials.LoadCCache(ccachePath)
	if err != nil {
		return nil, err
	}

	client, err := krbclient.NewFromCCache(ccache, krb5conf, settings...)
	if err != nil {
		return nil, err
	}

	return &Client{
		Client:        client,
		clientOptions: opts.normalized(),
	}, nil
}

func (client *Client) Close() error {
	client.Client.Destroy()
	return nil
}

func (client *Client) DeleteSecContext() error {
	client.ekey = types.EncryptionKey{}
	client.Subkey = types.EncryptionKey{}
	return nil
}

func (client *Client) InitSecContext(target string, input []byte) ([]byte, bool, error) {
	return client.InitSecContextWithOptions(target, input, []int{})
}

func (client *Client) InitSecContextWithOptions(target string, input []byte, APOptions []int) ([]byte, bool, error) {
	gssapiFlags := client.clientOptions.contextFlags()

	switch input {
	case nil:
		tkt, ekey, err := client.Client.GetServiceTicket(target)
		if err != nil {
			return nil, false, err
		}
		client.ekey = ekey

		token, err := spnego.NewKRB5TokenAPREQ(client.Client, tkt, ekey, gssapiFlags, APOptions)
		if err != nil {
			return nil, false, err
		}

		output, err := token.Marshal()
		if err != nil {
			return nil, false, err
		}

		return output, client.clientOptions.needInitContinuation(), nil

	default:
		var token spnego.KRB5Token

		err := token.Unmarshal(input)
		if err != nil {
			return nil, false, err
		}

		var completed bool

		if token.IsAPRep() {
			completed = true

			encpart, err := crypto.DecryptEncPart(token.APRep.EncPart, client.ekey, keyusage.AP_REP_ENCPART)
			if err != nil {
				return nil, false, err
			}

			part := &messages.EncAPRepPart{}

			if err = part.Unmarshal(encpart); err != nil {
				return nil, false, err
			}
			client.Subkey = part.Subkey
		}

		if token.IsKRBError() {
			return nil, !false, token.KRBError
		}

		return make([]byte, 0), !completed, nil
	}
}

func (client *Client) NegotiateSaslAuth(input []byte, authzid string) ([]byte, error) {
	token := &gssapi.WrapToken{}
	err := UnmarshalWrapToken(token, input, true)
	if err != nil {
		return nil, err
	}

	if (token.Flags & 0b1) == 0 {
		return nil, fmt.Errorf("got a Wrapped token that's not from the server")
	}

	key := client.ekey
	if (token.Flags & 0b100) != 0 {
		key = client.Subkey
	}

	_, err = token.Verify(key, keyusage.GSSAPI_ACCEPTOR_SEAL)
	if err != nil {
		return nil, fmt.Errorf("checksum mismatch: %w", err)
	}

	pl := token.Payload
	if len(pl) != 4 {
		return nil, fmt.Errorf("server send bad final token for SASL GSSAPI Handshake")
	}

	// McpCTL currently completes LDAP GSSAPI binds in auth-only mode.
	b := [4]byte{0, 0, 0, 0}
	payload := append(b[:], []byte(authzid)...)

	encType, err := crypto.GetEtype(key.KeyType)
	if err != nil {
		return nil, err
	}

	token = &gssapi.WrapToken{
		Flags:     0b100,
		EC:        uint16(encType.GetHMACBitLength() / 8),
		RRC:       0,
		SndSeqNum: 1,
		Payload:   payload,
	}

	if err := token.SetCheckSum(key, keyusage.GSSAPI_INITIATOR_SEAL); err != nil {
		return nil, err
	}

	output, err := token.Marshal()
	if err != nil {
		return nil, err
	}

	return output, nil
}

func getGssWrapTokenID() *[2]byte {
	return &[2]byte{0x05, 0x04}
}

func UnmarshalWrapToken(wt *gssapi.WrapToken, b []byte, expectFromAcceptor bool) error {
	if len(b) < 16 {
		return errors.New("bytes shorter than header length")
	}
	if !bytes.Equal(getGssWrapTokenID()[:], b[0:2]) {
		return fmt.Errorf("wrong Token ID. Expected %s, was %s",
			hex.EncodeToString(getGssWrapTokenID()[:]),
			hex.EncodeToString(b[0:2]))
	}
	flags := b[2]
	isFromAcceptor := flags&0x01 == 1
	if isFromAcceptor && !expectFromAcceptor {
		return errors.New("unexpected acceptor flag is set: not expecting a token from the acceptor")
	}
	if !isFromAcceptor && expectFromAcceptor {
		return errors.New("expected acceptor flag is not set: expecting a token from the acceptor, not the initiator")
	}
	if b[3] != gssapi.FillerByte {
		return fmt.Errorf("unexpected filler byte: expecting 0xFF, was %s ", hex.EncodeToString(b[3:4]))
	}
	checksumL := binary.BigEndian.Uint16(b[4:6])
	if int(checksumL) > len(b)-gssapi.HdrLen {
		return fmt.Errorf("inconsistent checksum length: %d bytes to parse, checksum length is %d", len(b), checksumL)
	}

	wt.Flags = flags
	wt.EC = checksumL
	wt.RRC = binary.BigEndian.Uint16(b[6:8])
	wt.SndSeqNum = binary.BigEndian.Uint64(b[8:16])
	wt.Payload = b[gssapi.HdrLen : len(b)-int(checksumL)]
	wt.CheckSum = b[len(b)-int(checksumL):]

	return nil
}
