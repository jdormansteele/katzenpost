package cryptoworker

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/katzenpost/hpqc/kem"
	"github.com/katzenpost/hpqc/nike/x25519"
	"github.com/katzenpost/hpqc/sign"
	"github.com/stretchr/testify/require"

	"github.com/katzenpost/katzenpost/core/log"
	"github.com/katzenpost/katzenpost/core/sphinx/constants"
	"github.com/katzenpost/katzenpost/core/sphinx/geo"
	"github.com/katzenpost/katzenpost/core/thwack"
	"github.com/katzenpost/katzenpost/core/wire"
	"github.com/katzenpost/katzenpost/loops"
	"github.com/katzenpost/katzenpost/server/config"
	"github.com/katzenpost/katzenpost/server/internal/glue"
	"github.com/katzenpost/katzenpost/server/internal/mixkeys"
	"github.com/katzenpost/katzenpost/server/internal/packet"
	"github.com/katzenpost/katzenpost/server/internal/pkicache"
	"github.com/katzenpost/katzenpost/server/spool"
	"github.com/katzenpost/katzenpost/server/userdb"
)

type mockServer struct {
	cfg               *config.Config
	logBackend        *log.Backend
	identityKey       sign.PrivateKey
	identityPublicKey sign.PublicKey
	linkKey           kem.PrivateKey
	mixKeys           glue.MixKeys
	pki               glue.PKI
	gateway           glue.Gateway
	service           glue.ServiceNode
	scheduler         glue.Scheduler
	connector         glue.Connector
	listeners         []glue.Listener
}

type mockGlue struct {
	s     *mockServer
	decoy *mockDecoy
}

func (g *mockGlue) Config() *config.Config {
	return g.s.cfg
}

func (g *mockGlue) LogBackend() *log.Backend {
	return g.s.logBackend
}

func (g *mockGlue) IdentityKey() sign.PrivateKey {
	return g.s.identityKey
}

func (g *mockGlue) IdentityPublicKey() sign.PublicKey {
	return g.s.identityPublicKey
}

func (g *mockGlue) LinkKey() kem.PrivateKey {
	return g.s.linkKey
}

func (g *mockGlue) MixKeys() glue.MixKeys {
	return g.s.mixKeys
}

func (g *mockGlue) PKI() glue.PKI {
	return g.s.pki
}

func (g *mockGlue) Gateway() glue.Gateway {
	return g.s.gateway
}

func (g *mockGlue) ServiceNode() glue.ServiceNode {
	return g.s.service
}

func (g *mockGlue) Scheduler() glue.Scheduler {
	return g.s.scheduler
}

func (g *mockGlue) Connector() glue.Connector {
	return g.s.connector
}

func (g *mockGlue) Listeners() []glue.Listener {
	return g.s.listeners
}

func (g *mockGlue) ReshadowCryptoWorkers() {}

func (g *mockGlue) Decoy() glue.Decoy {
	return g.decoy
}

func (m *mockGlue) Management() *thwack.Server {
	return nil
}

type mockDecoy struct {
	count int
}

func (d *mockDecoy) Halt() {}

func (d *mockDecoy) ExpectReply(pkt *packet.Packet) bool {
	return false
}

func (d *mockDecoy) OnNewDocument(*pkicache.Entry) {}

func (d *mockDecoy) OnPacket(*packet.Packet) {}

func (d *mockDecoy) GetStats(doPublishEpoch uint64) *loops.LoopStats {
	return nil
}

type mockScheduler struct {
	count int
}

func (s *mockScheduler) Halt() {}

func (s *mockScheduler) OnNewMixMaxDelay(delay uint64) {}

func (s *mockScheduler) OnPacket(pkt *packet.Packet) {
	s.count++
}

type mockService struct {
	count int
}

func (s *mockService) Halt() {}

func (s *mockService) OnPacket(*packet.Packet) {
	s.count++
}

func (s *mockService) KaetzchenForPKI() (map[string]map[string]interface{}, error) {
	return nil, nil
}

type mockGateway struct {
	count int

	userName string
	userKey  kem.PublicKey
}

func (p *mockGateway) Halt() {}

func (p *mockGateway) UserDB() userdb.UserDB {
	return &mockUserDB{
		gateway: p,
	}
}

func (p *mockGateway) Spool() spool.Spool {
	return &mockSpool{}
}

func (p *mockGateway) AuthenticateClient(*wire.PeerCredentials) bool {
	return true
}

func (p *mockGateway) OnPacket(*packet.Packet) {
	p.count++
}

type mockUserDB struct {
	gateway *mockGateway
}

func (u *mockUserDB) Exists([]byte) bool {
	return true
}

func (u *mockUserDB) IsValid([]byte, kem.PublicKey) bool { return true }

func (u *mockUserDB) Add([]byte, kem.PublicKey, bool) error { return nil }

func (u *mockUserDB) SetIdentity([]byte, kem.PublicKey) error { return nil }

func (u *mockUserDB) Link([]byte) (kem.PublicKey, error) {
	return nil, nil
}

func (u *mockUserDB) Identity([]byte) (kem.PublicKey, error) {
	return u.gateway.userKey, nil
}

func (u *mockUserDB) Remove([]byte) error { return nil }

func (u *mockUserDB) Close() {}

type mockSpool struct{}

func (s *mockSpool) StoreMessage(u, msg []byte) error { return nil }

func (s *mockSpool) StoreSURBReply(u []byte, id *[constants.SURBIDLength]byte, msg []byte) error {
	return nil
}

func (s *mockSpool) Get(u []byte, advance bool) (msg, surbID []byte, remaining int, err error) {
	return []byte{1, 2, 3}, nil, 1, nil
}

func (s *mockSpool) Remove(u []byte) error { return nil }

func (s *mockSpool) VacuumExpired(udb userdb.UserDB, ignoreIdentities map[[32]byte]interface{}) error {
	return nil
}

func (s *mockSpool) Vacuum(udb userdb.UserDB) error { return nil }

func (s *mockSpool) Close() {}

// routing results
const (
	SentToDecoy = iota
	SentToGateway
	SentToService
	SentToScheduler
	Dropped
)

func TestRoutePacket(t *testing.T) {
	nrHops := 5
	withSURB := true
	userForwardPayloadLength := 2000

	mygeo := geo.GeometryFromUserForwardPayloadLength(x25519.Scheme(rand.Reader), userForwardPayloadLength, withSURB, nrHops)

	mixNodeConfig := &config.Config{
		SphinxGeometry: mygeo,
		Server: &config.Server{
			IsGatewayNode: false,
			IsServiceNode: false,
		},
		Logging: &config.Logging{},
		PKI:     &config.PKI{},
		Debug: &config.Debug{
			NumKaetzchenWorkers: 3,
			KaetzchenDelay:      300,
		},
	}

	serviceNodeConfig := &config.Config{
		SphinxGeometry: mygeo,
		Server: &config.Server{
			IsServiceNode: true,
		},
		Logging: &config.Logging{},
		ServiceNode: &config.ServiceNode{
			Kaetzchen: []*config.Kaetzchen{
				&config.Kaetzchen{
					Capability: "echo",
					Endpoint:   "echo",
					Config:     map[string]interface{}{},
					Disable:    false,
				},
			},
		},
		PKI: &config.PKI{},
		Debug: &config.Debug{
			NumKaetzchenWorkers: 3,
			KaetzchenDelay:      300,
		},
	}

	gatewayNodeConfig := &config.Config{
		Gateway:        &config.Gateway{},
		SphinxGeometry: mygeo,
		Server: &config.Server{
			IsGatewayNode: true,
		},
		Logging: &config.Logging{},
		PKI:     &config.PKI{},
		Debug: &config.Debug{
			NumKaetzchenWorkers: 3,
			KaetzchenDelay:      300,
		},
	}

	testCases := []struct {
		nodeCfg       *config.Config
		inputPacket   *packet.Packet
		routingResult int
	}{
		// XXX FIX ME: write multiple test cases for each node type.
		{
			nodeCfg:       serviceNodeConfig,
			inputPacket:   packet1,
			routingResult: 1,
		},
		{
			nodeCfg:       gatewayNodeConfig,
			inputPacket:   packet1,
			routingResult: 1,
		},
		{
			nodeCfg:       mixNodeConfig,
			inputPacket:   packet1,
			routingResult: 1,
		},
	}

	for i := 0; i < len(testCases); i++ {
		result := testRouting(t, testCases[i].nodeCfg, testCases[i].inputPacket, mygeo)
		require.Equal(t, result, testCases[i].routingResult)
	}
}

func testRouting(t *testing.T, nodeCfg *config.Config, inputPacket *packet.Packet, mygeo *geo.Geometry) int {

	logBackend, err := log.New("", "DEBUG", false)
	require.NoError(t, err)

	goo := &mockGlue{
		decoy: new(mockDecoy),
		s: &mockServer{
			cfg:        nodeCfg,
			logBackend: logBackend,
			scheduler:  new(mockScheduler),
			gateway:    new(mockGateway),
			service:    new(mockService),
		},
	}

	mixkeys, err := mixkeys.NewMixKeys(goo, mygeo)
	require.NoError(t, err)

	goo.s.mixKeys = mixkeys
	incomingCh := make(chan interface{})
	cryptoworker := New(goo, incomingCh, 123)

	startAt := time.Now()
	pkt := &packet.Packet{}

	cryptoworker.routePacket(pkt, startAt)

	switch {
	case goo.s.scheduler.(*mockScheduler).count == 1:
		return SentToScheduler
	case goo.decoy.count == 1:
		return SentToDecoy
	case goo.s.gateway.(*mockGateway).count == 1:
		return SentToGateway
	case goo.s.service.(*mockService).count == 1:
		return SentToService
	default:
		return Dropped
	}

	// unreachable
}
