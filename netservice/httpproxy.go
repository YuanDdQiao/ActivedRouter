package netservice

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"ActivedRouter/cache"
	"ActivedRouter/global"
	"ActivedRouter/system"
	"ActivedRouter/tools"
)

var DefaultHttpReverseProxy = &HttpReverseProxy{Cfg: &ReverseProxyConfigData{}}

//Create new reverse proxy instance
func NewReverseProxy() *HttpReverseProxy {
	return &HttpReverseProxy{Cfg: &ReverseProxyConfigData{}}
}

type HostInfo struct {
	Port string `json:"port"`
	Host string `json:"host"`
}

//Load Balance Node
type LbNode struct {
	Domain      string      `json:"domain"`
	HttpsSwitch string      `json:"https_switch"`
	HttpSwitch  string      `json:"http_switch"`
	Clients     []*HostInfo `json:"clients"`
}

//ReverseProxy Config
type ReverseProxyConfigData struct {
	ProxyMethod   string `json:"proxy_method"`
	HttpProxyAddr string `json:"http_proxy_addr"`
	//global http switch
	GlobalHttpSwitch string `json:"http_switch"`
	//global https switch
	GlobalHttpsSwitch string                       `json:"https_switch"`
	HttpsProxyAddr    string                       `json:"https_proxy_addr"`
	ReverseProxy      []*LbNode                    `json:"reserve_proxy"`
	DomainProxySwitch map[string]map[string]string `json:"-"`
}

//reverse proxy handler
type HttpReverseProxy struct {
	DomainHostList cache.Cacher
	Cfg            *ReverseProxyConfigData
	httpsServer    *HttpsServer
	//certificate config
	CertificateConfigData []*CertificateConfig
	ProxyCongfigFile      string
	ProxyMethod           string
}

//domain list
func (self *HttpReverseProxy) DomainInfos() []string {
	data := *self.DomainHostList.GetStorage().GetData()
	keysArr := make([]string, 0)
	for k, _ := range data {
		keysArr = append(keysArr, k)
	}
	return keysArr
}

//Add the domain name to the configuration
func (self *HttpReverseProxy) AddDomainConfig(domain string) bool {
	for _, v := range self.Cfg.ReverseProxy {
		if v.Domain == domain {
			return false
		}
	}
	self.Cfg.ReverseProxy = append(self.Cfg.ReverseProxy, &LbNode{Domain: domain})
	if self.SaveToFile() {
		//Hot update
		self.DomainHostList.Set(domain, []*HostInfo{})
		return true
	}
	return false
}

//save to file
func (this *HttpReverseProxy) SaveToFile() bool {
	if bts, err := json.MarshalIndent(this.Cfg, "", " "); err != nil {
		return false
	} else {
		if file, err := os.OpenFile(this.ProxyCongfigFile, os.O_RDWR|os.O_TRUNC, os.ModePerm); err != nil {
			defer file.Close()
			return false
		} else {
			if _, err := file.Write(bts); err != nil {
				return false
			}
		}
	}
	return true
}

//Delete the domain name and sync to the configuration file
func (self *HttpReverseProxy) DeleteDomainConig(domain string) bool {
	for k, v := range self.Cfg.ReverseProxy {
		if v.Domain == domain {
			//delete item
			ret, _ := tools.DeleteSlice(self.Cfg.ReverseProxy, k)
			self.Cfg.ReverseProxy = ret.([]*LbNode)
			//hot update
			self.DomainHostList.Del(domain)
			self.SaveToFile()
		}
	}
	return false
}

//delete reverse proxydomain
func (self *HttpReverseProxy) DeleteProxyClient(domain, hostip, port string) bool {
	for _, v := range self.Cfg.ReverseProxy {
		if v.Domain == domain {
			for index, client := range v.Clients {
				if client.Host == hostip && client.Port == port {
					//delete item
					ret, _ := tools.DeleteSlice(v.Clients, index)
					v.Clients = ret.([]*HostInfo)
					//hot update
					if self.DomainHostList.Has(domain) {
						clientInfoList := self.GetDomainHostList(domain)
						for index, item := range clientInfoList {
							if item.Host == hostip && item.Port == port {
								resultSlice, _ := tools.DeleteSlice(clientInfoList, index)
								self.DomainHostList.Set(domain, resultSlice)
							}
						}
					}
					if self.SaveToFile() {
						return true
					} else {
						return false
					}
				}
			}
		}
	}
	return false
}

//Update Reverse Proxy Client Info
func (self *HttpReverseProxy) UpdateProxyClient(domain, preHost, prePort, updateHost, updatePort, httpsSwitch, httpSwitch string) bool {
	for _, v := range self.Cfg.ReverseProxy {
		if v.Domain == domain {
			v.HttpsSwitch = httpsSwitch
			v.HttpSwitch = httpSwitch
			for _, client := range v.Clients {
				if client.Host == preHost && client.Port == prePort {
					client.Host = updateHost
					client.Port = updatePort
					//hot update
					if self.DomainHostList.Has(domain) {
						clientInfoList := self.GetDomainHostList(domain)
						for _, item := range clientInfoList {
							if item.Host == preHost && item.Port == prePort {
								item.Host = updateHost
								item.Port = updatePort
							}
						}
					}
					if self.SaveToFile() {
						return true
					} else {
						return false
					}
				}
			}
		}
	}
	return true
}

//Add the reverse proxy client to the specified domain name
//Return Value
// -1  Repeat
//  0  Failure
//  1  Success
func (self *HttpReverseProxy) AddProxyClient(domain, hostip, port, httsSwitch, httpSwitch string) int {
	for _, v := range self.Cfg.ReverseProxy {
		if v.Domain == domain {
			//proxy switch
			v.HttpsSwitch = httsSwitch
			v.HttpSwitch = httpSwitch
			for _, client := range v.Clients {
				if client.Host == hostip && client.Port == port {
					return -1
				}
			}
			//Add the domain name repeatedly!
			v.Clients = append(v.Clients, &HostInfo{port, hostip})
			//hot update
			if !self.DomainHostList.Has(domain) {
				self.DomainHostList.Set(domain, []*HostInfo{&HostInfo{port, hostip}})
			} else {
				clientList, _ := self.DomainHostList.Get(domain)
				clientInfoList, _ := clientList.([]*HostInfo)
				self.DomainHostList.Set(domain, append(clientInfoList, &HostInfo{port, hostip}))
			}
			if self.SaveToFile() {
				return 1
			} else {
				return 0
			}
		}
	}
	self.Cfg.ReverseProxy = append(self.Cfg.ReverseProxy, &LbNode{Domain: domain, HttpsSwitch: "off", Clients: []*HostInfo{&HostInfo{port, hostip}}})
	self.SaveToFile()
	return 1
}

//update domain
func (this *HttpReverseProxy) UpdateDomain(preDomain, updateDomain, httpsSwitch, httpSwitch string) bool {
	for _, v := range this.Cfg.ReverseProxy {
		if v.Domain == preDomain {
			//proxy switch
			v.HttpsSwitch = httpsSwitch
			v.HttpSwitch = httpSwitch
			v.Domain = updateDomain
			//hot update
			data, _ := this.DomainHostList.Get(preDomain)
			this.DomainHostList.Del(preDomain)
			this.DomainHostList.Set(updateDomain, data)
			if this.SaveToFile() {
				return true
			} else {
				return false
			}
		}
	}
	return true
}

func (self *HttpReverseProxy) ChangeSwitchStatus(domain, protocol, switchStatus string) {
	for _, v := range self.Cfg.ReverseProxy {
		if v.Domain == domain {
			switch protocol {
			case "http":
				{
					v.HttpSwitch = switchStatus
				}
			case "https":
				{
					v.HttpsSwitch = switchStatus
				}
			default:
				{
					return
				}
			}
			self.SaveToFile()
		}
	}
}

//hostlist by domain
func (self *HttpReverseProxy) GetDomainHostList(domain string) []*HostInfo {
	v, _ := self.DomainHostList.Get(domain)
	vArr, _ := v.([]*HostInfo)
	return vArr
}

//random method
func (self *HttpReverseProxy) getRandomHost(domain string) *HostInfo {
	v, _ := self.DomainHostList.Get(domain)
	vArr, _ := v.([]*HostInfo)
	proxyCount := len(vArr)
	//fix bug :integer divide by zero
	if proxyCount == 0 {
		return nil
	}
	index := rand.Uint32() % uint32(proxyCount)
	return vArr[index]
}

//alived method
//According to the domain name or ip to obtain the most active cluster host
func (self *HttpReverseProxy) getAlivedHost(domain string) *HostInfo {
	v, _ := self.DomainHostList.Get(domain)
	vArr, _ := v.([]*HostInfo)
	hostinfo := self.bestHostInfo(vArr)
	return hostinfo
}

func (self *HttpReverseProxy) bestHostInfo(hosts []*HostInfo) *HostInfo {
	hostSortedList := global.GHostInfoTable.ActiveHostWeightList
	for el := hostSortedList.Front(); el != nil; el = el.Next() {
		bestHost := el.Value.(system.HostInfo)
		for _, host := range hosts {
			if bestHost.Info.IP == host.Host || bestHost.Info.Domain == host.Host {
				return host
			}
		}
	}
	return nil
}

//proxy_method  random  and alived
func (self *HttpReverseProxy) getHostInfo(host, proxyMethod string) *HostInfo {
	requestHost := host
	//Handle non-80 ports
	if strings.IndexAny(host, ":") != -1 {
		strs := strings.Split(host, ":")
		requestHost = strs[0]
	}
	//random
	//alived
	switch proxyMethod {
	case global.Random:
		{
			return self.getRandomHost(requestHost)
		}
	case global.Alived:
		{
			return self.getAlivedHost(requestHost)
		}
	}
	return nil
}

//Http and https access filters
//If the request protocol is https, check whether the reverse proxy is allowed to pass
func (self *HttpReverseProxy) accessFilter(w http.ResponseWriter, r *http.Request) bool {
	//global https http switch
	if r.TLS != nil {
		if !self.httpsServer.checkValidHttpsReq(r.Host) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(r.Host + "&nbsp;&nbsp;can't be accessed via https,please configure a digital certificate........."))
		}
		if self.Cfg.GlobalHttpsSwitch == global.SwitchOff {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(r.Host + "&nbsp;&nbsp;Please open global https proxy switch........."))
		} else if self.Cfg.GlobalHttpsSwitch == global.SwitchOn {
			if proxySwitch, ok := self.Cfg.DomainProxySwitch[r.Host]; ok {
				if httpsSwitch, ok := proxySwitch["https"]; ok {
					if httpsSwitch == global.SwitchOn {
						return true
					}
				}
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(r.Host + "&nbsp;&nbsp;Please open https proxy switch........."))
			}
		}
	} else {
		if self.Cfg.GlobalHttpSwitch == global.SwitchOff {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(r.Host + "&nbsp;&nbsp;Please open global http proxy switch........."))
		} else {
			if proxySwitch, ok := self.Cfg.DomainProxySwitch[r.Host]; ok {
				if httpsSwitch, ok := proxySwitch["http"]; ok {
					if httpsSwitch == global.SwitchOn {
						return true
					}
				}
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(r.Host + "&nbsp;&nbsp;Please open http proxy switch........."))
			}
		}
	}
	return false
}

//Http and Https reverse proxy handeler
func (self *HttpReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !self.accessFilter(w, r) {
		return
	}
	//Get the business server
	hostinfo := self.getHostInfo(r.Host, self.ProxyMethod)
	if hostinfo == nil {
		//If you can't get the active host then use the random method。
		hostinfo = self.getHostInfo(r.Host, global.Random)
		if hostinfo == nil {
			w.Write([]byte(r.Host + "Can't find active server........."))
			return
		}
	}
	//Redirect http request
	redirect := fmt.Sprintf("http://%s:%s", hostinfo.Host, hostinfo.Port)
	remote, err := url.Parse(redirect)
	if err != nil {
		panic(err)
	}
	// Not modifyed the http request header
	proxy := httputil.NewSingleHostReverseProxy(remote)
	proxy.ServeHTTP(w, r)
	//Update reverse proxy statistics
	go global.GProxyHttpStatistics.UpdateClusterStatistics(r.Host, 0)
}

//Load Certificate Config
func (self *HttpReverseProxy) LoadCertificateConfig(certificateConfigFile string) {
	stat, err := os.Stat(global.CertificateData)
	if os.IsNotExist(err) || !stat.IsDir() {
		return
	}
	if fileInfos, err := ioutil.ReadDir(global.CertificateData); err != nil {
		return
	} else {
		for _, fileInfo := range fileInfos {
			if fileInfo.IsDir() {
				domain := fileInfo.Name()
				certFile := path.Join(global.CertificateData, domain, global.DefaultCertificate)
				keyFile := path.Join(global.CertificateData, domain, global.DefaultKey)
				self.CertificateConfigData = append(self.CertificateConfigData, &CertificateConfig{Domain: domain, CertFile: certFile, KeyFile: keyFile})
			}
		}
	}
}

//Load proxy config
func (self *HttpReverseProxy) LoadProxyConfig(proxyConfigFile string) {
	var httpAddr, httpsAddr, httpSwitch, httpsSwitch string
	self.ProxyCongfigFile = proxyConfigFile
	file, err := os.Open(proxyConfigFile)
	defer file.Close()
	if err != nil {
		log.Fatalln(err.Error())
	}
	if bts, err := ioutil.ReadAll(file); err != nil {
		log.Fatalln(err.Error())
	} else {
		if err := json.Unmarshal(bts, &self.Cfg); err != nil {
			log.Fatalln("Parse proxy config file .....")
		}
		self.Cfg.DomainProxySwitch = make(map[string]map[string]string)
		httpSwitch = self.Cfg.GlobalHttpSwitch
		httpsSwitch = self.Cfg.GlobalHttpsSwitch
		//http https  off
		if httpSwitch != global.SwitchOn && httpsSwitch != global.SwitchOn {
			log.Fatalln("Please open http or https reverse proxy switch.....")
		}
		//Get the http switch
		if httpSwitch == global.SwitchOn {
			if self.Cfg.HttpProxyAddr == "" {
				httpAddr = global.DefaultHttpAddr
			} else {
				httpAddr = self.Cfg.HttpProxyAddr
			}
			log.Println("Http Switch:" + httpSwitch)
			log.Println("Http  Addr:" + httpAddr)
		}
		//Get the https switch
		if httpsSwitch == global.SwitchOn {
			if self.Cfg.HttpsProxyAddr == "" {
				httpsAddr = global.DefaultHttsAddr
			} else {
				httpsAddr = self.Cfg.HttpsProxyAddr
			}
			log.Println("Https Switch:" + httpsSwitch)
			log.Println("Https Addr:" + httpsAddr)
		}
		//Proxy method
		if self.Cfg.ProxyMethod == "" {
			self.ProxyMethod = global.Random
		} else {
			self.ProxyMethod = self.Cfg.ProxyMethod
		}
		//Create a memory cache to store the list of domain names
		self.DomainHostList = cache.Newcache("memory")
		clients := self.Cfg.ReverseProxy
		for _, client := range clients {
			subDomain := client.Domain
			//Domain proxy switch
			self.Cfg.DomainProxySwitch[subDomain] = map[string]string{"http": client.HttpSwitch, "https": client.HttpsSwitch}
			var subClientList []*HostInfo
			for _, hostInfo := range client.Clients {
				subClientList = append(subClientList, hostInfo)
			}
			self.DomainHostList.Set(subDomain, subClientList)
		}
	}
}

//Run the http statistics service
func (self *HttpReverseProxy) BeginHttpStatistics() {
	timerStatistics := time.NewTimer(time.Second * global.Http_Statistics_Interval)
	for {
		select {
		case <-timerStatistics.C:
			{
				//reset timer
				timerStatistics.Reset(time.Second * global.Http_Statistics_Interval)
				//Incremental statistical curve(曲线)
				global.GProxyHttpStatistics.UpdateClusterStatistics("", 1)
			}
		}
	}
}

//start all https service
func (self *HttpReverseProxy) StartAllHttpsService() bool {
	self.Cfg.GlobalHttpsSwitch = global.SwitchOn
	return self.SaveToFile()
}

//stop all  https service
func (self *HttpReverseProxy) StopAllHttpsService() bool {
	self.Cfg.GlobalHttpsSwitch = global.SwitchOff
	return self.SaveToFile()
}

//start all  http service
func (self *HttpReverseProxy) StartAllHttpService() bool {
	self.Cfg.GlobalHttpSwitch = global.SwitchOn
	return self.SaveToFile()
}

//stop all http service
func (self *HttpReverseProxy) StoptAllHttpService() bool {
	self.Cfg.GlobalHttpSwitch = global.SwitchOff
	return self.SaveToFile()
}

//Run Reverse Proxy
func (self *HttpReverseProxy) StartProxyServer() {
	//Http service switch
	if self.Cfg.GlobalHttpSwitch == global.SwitchOn {
		go func() {
			err := http.ListenAndServe(self.Cfg.HttpProxyAddr, DefaultHttpReverseProxy)
			if err != nil {
				log.Fatalln("ListenAndServe HTTP: ", err)
			} else {
				log.Println("Listen Http :", self.Cfg.HttpProxyAddr)
			}
		}()
	}
	//Https  service switch
	if self.Cfg.GlobalHttpsSwitch == global.SwitchOn {
		go func() {
			self.httpsServer = NewHttpsServer()
			self.httpsServer.AddDomainCertificateConfig(self.CertificateConfigData)
			err := self.httpsServer.RunHttpsService(self.Cfg.HttpsProxyAddr, "", "", self)
			if err != nil {
				log.Fatalln("RunHttpServer:", err)
			} else {
				log.Println("Listen Http SSL:", self.Cfg.HttpsProxyAddr)
			}
		}()
	}
	//Open http reverse proxy statistics
	//You can choose whether to open, because this option will affect the http request speed,
	// you can turn off.
	go self.BeginHttpStatistics()
}
