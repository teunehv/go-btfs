package analytics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/TRON-US/go-btfs/core"
	"github.com/ipfs/go-bitswap"
	peer "github.com/libp2p/go-libp2p-peer"

	"github.com/shirou/gopsutil/cpu"
)

type programInfo struct {
	node        *core.IpfsNode
	startTime   time.Time //Time at which the Daemon was ready and analytics started
	NodeID      string    `json:"node_id"`
	CPUInfo     string    `json:"cpu_info"`
	BTFSVersion string    `json:"btfs_version"`
	OSType      string    `json:"os_type"`
	ArchType    string    `json:"arch_type"`
}

type dataCollection struct {
	programInfo
	UpTime      uint64  `json:"up_time"`         //Seconds
	StorageUsed uint64  `json:"storage_used"`    //Stored in Kilobytes
	MemUsed     uint64  `json:"memory_used"`     //Stored in Kilobytes
	CPUUsed     float64 `json:"cpu_used"`        //Overall CPU used
	Upload      uint64  `json:"upload"`          //Upload over last epoch, stored in Kilobytes
	Download    uint64  `json:"download"`        //Download over last epoch, stored in Kilobytes
	TotalUp     uint64  `json:"total_upload"`    //Total data up, Stored in Kilobytes
	TotalDown   uint64  `json:"total_download"`  //Total data down, Stored in Kilobytes
	BlocksUp    uint64  `json:"blocks_up"`       //Total num of blocks uploaded
	BlocksDown  uint64  `json:"blocks_down"`     //Total num of blocks downloaded
	Exchanges   uint64  `json:"exchanges"`       //Number of block exchanges
	NumPeers    uint64  `json:"peers_connected"` //Number of peers
}

const kilobyte = 1024

//HeartBeat is how often we send data to server, at the moment set to 15 Minutes
var heartBeat = 15 * time.Minute

//Server URL for data collection
var dataServeURL = "http://18.220.204.165:8080/metrics"

var peerCountMap map[peer.ID]uint64

//Go doesn't have a built in Max function? simple function to not have negatives values
func valOrZero(x uint64) uint64 {
	if x < 0 {
		return 0
	}

	return x
}

func durationToSeconds(duration time.Duration) uint64 {
	return uint64(duration.Nanoseconds() / int64(time.Second/time.Nanosecond))
}

//Initialize starts the process to collect data and starts the GoRoutine for constant collection
func Initialize(n *core.IpfsNode, BTFSVersion string) {
	dc := new(dataCollection)
	infoStats, _ := cpu.Info()

	dc.node = n
	dc.startTime = time.Now()
	dc.NodeID = n.Identity.Pretty()
	dc.CPUInfo = infoStats[0].ModelName
	dc.BTFSVersion = BTFSVersion
	dc.OSType = runtime.GOOS
	dc.ArchType = runtime.GOARCH

	peerCountMap = make(map[peer.ID]uint64)

	go dc.collectionAgent()
}

func (dc *dataCollection) update() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	cpus, _ := cpu.Percent(0, false)
	storage, _ := dc.node.Repo.GetStorageUsage()

	dc.UpTime = durationToSeconds(time.Since(dc.startTime))
	dc.CPUUsed = cpus[0]
	dc.MemUsed = m.HeapAlloc / kilobyte
	dc.StorageUsed = storage / kilobyte

	bs, ok := dc.node.Exchange.(*bitswap.Bitswap)
	if !ok {
		return
	}

	st, err := bs.Stat()
	if err != nil {
		return
	}

	dc.Upload = valOrZero(st.DataSent-dc.TotalUp) / kilobyte
	dc.Download = valOrZero(st.DataReceived-dc.TotalDown) / kilobyte
	dc.TotalUp = st.DataSent / kilobyte
	dc.TotalDown = st.DataReceived / kilobyte
	dc.BlocksUp = st.BlocksSent
	dc.BlocksDown = st.BlocksReceived

	//This iterates over all peers connected on ledger, this might end up being prohibitively
	//expensive in the future. Better would be maintain a counter like the other stats
	var exchangeCount uint64
	for _, peerString := range st.Peers {
		peerID, err := peer.IDB58Decode(peerString)
		if err != nil {
			fmt.Println(err)
			return
		}
		peerRec := bs.LedgerForPeer(peerID)
		exchangeCount += peerRec.Exchanged - peerCountMap[peerID]
		peerCountMap[peerID] = peerRec.Exchanged
	}

	dc.Exchanges = exchangeCount
	dc.NumPeers = uint64(len(st.Peers))
}

func (dc *dataCollection) sendData() {
	dc.update()
	temp, _ := json.Marshal(dc)

	req, err := http.NewRequest("POST", dataServeURL, bytes.NewReader(temp))
	req.Header.Add("Content-Type", "application/json")
	if err != nil {
		return
	}

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return
	}

	defer res.Body.Close()
}

func (dc *dataCollection) collectionAgent() {
	tick := time.NewTicker(heartBeat)

	defer tick.Stop()
	dc.sendData()

	for range tick.C {
		dc.sendData()
	}
}