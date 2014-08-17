package main

import (
	"encoding/csv"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxsubnet = 22 //openresolverproject.org allows for /22 max
const numtasks = 16

var wg sync.WaitGroup

type ScanResult struct {
	IPqueried    string
	RespondingIP string
	Time_t       time.Time
	Rcode        int
	Recursion    bool
	Correct      bool
}

func (self *ScanResult) String() string {
	return fmt.Sprintf("%s\t%s\t%s\t%d\t%v\t%v", self.IPqueried, self.RespondingIP, self.Time_t, self.Rcode, self.Recursion, self.Correct)
}

type tmprow struct {
	Td []string `xml:"TD"`
}

func scanrow(row string) (result *ScanResult, err error) {
	result = &ScanResult{}
	//fmt.Println(row)
	v := tmprow{}
	err = xml.Unmarshal([]byte(row), &v)
	if err != nil {
		return
	}
	result.IPqueried = v.Td[0]
	result.RespondingIP = v.Td[1]
	time_t, err := strconv.Atoi(v.Td[2])
	if err != nil {
		return
	} else {
		result.Time_t = time.Unix(int64(time_t), 0)
	}
	result.Rcode, err = strconv.Atoi(v.Td[3])
	if err != nil {
		return
	}
	result.Recursion, err = strconv.ParseBool(v.Td[4])
	if err != nil {
		return
	}
	result.Correct, err = strconv.ParseBool(v.Td[5])
	if err != nil {
		return
	}
	//fmt.Println(v)
	return
}

func inttoIPv4(a int) net.IP {
	return net.IPv4(byte(a>>24), byte(a>>16), byte(a>>8), byte(a))
}

func findidealmask(start int, mask int) int {
	if mask > 32 {
		panic("mask cant be > 32")
	}
	pow := math.Pow(2, float64(32-mask))
	if math.Mod(float64(start), pow) == 0 {
		return mask
	} else {
		return findidealmask(start, mask+1)
	}
}

func parseiprange(start, end string) (nets []string, err error) {
	startint, err := strconv.Atoi(start)
	if err != nil {
		return
	}
	endint, err := strconv.Atoi(end)
	if err != nil {
		return
	}
	ideal := findidealmask(startint, maxsubnet)
	count := 0
	delta := int(math.Pow(2, float64(32-ideal)))
	for {
		count += delta
		n := fmt.Sprintf("%s/%d", inttoIPv4(startint+count), ideal)
		//fmt.Println(n)
		nets = append(nets, n)
		if startint+count > endint {
			break
		}
	}
	//fmt.Println(inttoIPv4(startint),  inttoIPv4(endint))
	return
}

func scanner(taskchan chan string, resultchan chan *ScanResult) {
	defer wg.Done()
	for n := range taskchan {
		scanrange(n, resultchan)
		fmt.Println("Done", n)
	}
	fmt.Println("taskended")
	return
}

func scanrange(n string, resultchan chan *ScanResult) {
	url := fmt.Sprintf("http://openresolverproject.org/search2.cgi?botnet=yessir&search_for=%s", strings.Replace(n, "/", "%2F", 1))
	//fmt.Println(url)
	cj, err := cookiejar.New(nil)
	if err != nil {
		return
	}
	client := &http.Client{Jar: cj}
	/*
		req, err := http.NewRequest("GET","http://openresolverproject.org", nil)
		if err != nil {
			return
		}
		//req.Header.Set("User-Agent", "http://openresolverproject.org")
		resp, err := client.Do(req)
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return
		}
	*/
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Referer", "http://openresolverproject.org")
	resp, err := client.Do(req)

	//	resp, err := http.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}
	//fmt.Println(string(body))

	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "<TR>") {
			if !strings.Contains(line, "IP Queried") {
				result, err := scanrow(line)
				if err != nil {
					fmt.Println(err)
				} else {
					resultchan <- result
				}
			}
		}
	}
	return
}

func main() {
	var maxmindasnum = flag.String("maxmind", "/home/sajal/Downloads/GeoIPASNum2.csv", "path to GeoIPASNum2.csv")
	var asn = flag.String("asn", "AS17552", "ASN to query for")
	flag.Parse()
	fmt.Println("Using", *maxmindasnum, "to search for", *asn)
	f, err := os.Open(*maxmindasnum)
	if err != nil {
		panic(err)
	}
	rdr := csv.NewReader(f)
	var scanlist []string
	for {
		record, err := rdr.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			panic(err)
		}
		if strings.HasPrefix(record[2], *asn) {
			fmt.Println(record)
			sl, err := parseiprange(record[0], record[1])
			if err != nil {
				panic(err)
			}
			scanlist = append(scanlist, sl...)
		}
	}
	taskchan := make(chan string, numtasks)
	resultchan := make(chan *ScanResult)
	wg.Add(numtasks)
	for i := 0; i < numtasks; i++ {
		go scanner(taskchan, resultchan)
		fmt.Println("Starting worker", i)
	}
	go func() {
		var results []*ScanResult
		for res := range resultchan {
			results = append(results, res)
			fmt.Println(res)
		}
		fmt.Println("========================")
		for _, res := range results {
			fmt.Println(res)
		}
		wg.Done()
	}()
	count := 0
	for _, n := range scanlist {
		taskchan <- n
		count += 1
		fmt.Println(count, "/", len(scanlist), n)
	}
	close(taskchan)
	wg.Wait()
	close(resultchan)
	wg.Add(1)
	wg.Wait()
}
