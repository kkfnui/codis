// Copyright 2016 CodisLabs. All Rights Reserved.
// Licensed under the MIT (MIT-LICENSE.txt) license.

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/docopt/docopt-go"

	"github.com/CodisLabs/codis/pkg/models"
	"github.com/CodisLabs/codis/pkg/topom"
	"github.com/CodisLabs/codis/pkg/utils"
	"github.com/CodisLabs/codis/pkg/utils/log"
)

func main() {
	const usage = `
Usage:
	codis-dashboard [--ncpu=N] [--config=CONF] [--log=FILE] [--log-level=LEVEL] [--host-admin=ADDR] [--zookeeper=ADDR|--etcd=ADDR|--filesystem=ROOT|--fillslots=FILE] [--pidfile=FILE]
	codis-dashboard  --default-config
	codis-dashboard  --version

Options:
	--ncpu=N                    set runtime.GOMAXPROCS to N, default is runtime.NumCPU().
	-c CONF, --config=CONF      run with the specific configuration.
	-l FILE, --log=FILE         set path/name of daliy rotated log file.
	--log-level=LEVEL           set the log-level, should be INFO,WARN,DEBUG or ERROR, default is INFO.
`

	d, err := docopt.Parse(usage, nil, true, "", false)
	if err != nil {
		log.PanicError(err, "parse arguments failed")
	}

	switch {

	case d["--default-config"]:
		fmt.Println(topom.DefaultConfig)
		return

	case d["--version"].(bool):
		fmt.Println("version:", utils.Version)
		fmt.Println("compile:", utils.Compile)
		return

	}

	if s, ok := utils.Argument(d, "--log"); ok {
		w, err := log.NewRollingFile(s, log.DailyRolling)
		if err != nil {
			log.PanicErrorf(err, "open log file %s failed", s)
		} else {
			log.StdLog = log.New(w, "")
		}
	}
	log.SetLevel(log.LevelInfo)

	if s, ok := utils.Argument(d, "--log-level"); ok {
		if !log.SetLevelString(s) {
			log.Panicf("option --log-level = %s", s)
		}
	}

	if n, ok := utils.ArgumentInteger(d, "--ncpu"); ok {
		runtime.GOMAXPROCS(n)
	} else {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}
	log.Warnf("set ncpu = %d", runtime.GOMAXPROCS(0))

	config := topom.NewDefaultConfig()
	if s, ok := utils.Argument(d, "--config"); ok {
		if err := config.LoadFromFile(s); err != nil {
			log.PanicErrorf(err, "load config %s failed", s)
		}
	}
	if s, ok := utils.Argument(d, "--host-admin"); ok {
		config.HostAdmin = s
		log.Warnf("option --host-admin = %s", s)
	}

	var coordinator struct {
		name string
		addr string
	}

	switch {

	case d["--zookeeper"] != nil:
		coordinator.name = "zookeeper"
		coordinator.addr = utils.ArgumentMust(d, "--zookeeper")

	case d["--etcd"] != nil:
		coordinator.name = "etcd"
		coordinator.addr = utils.ArgumentMust(d, "--etcd")

	case d["--filesystem"] != nil:
		coordinator.name = "filesystem"
		coordinator.addr = utils.ArgumentMust(d, "--filesystem")

	}

	if coordinator.name != "" {
		log.Warnf("option --%s = %s", coordinator.name, coordinator.addr)
		config.CoordinatorName = coordinator.name
		config.CoordinatorAddr = coordinator.addr
	}

	client, err := models.NewClient(config.CoordinatorName, config.CoordinatorAddr, time.Minute)
	if err != nil {
		log.PanicErrorf(err, "create '%s' client to '%s' failed", config.CoordinatorName, config.CoordinatorAddr)
	}
	defer client.Close()

	s, err := topom.New(client, config)
	if err != nil {
		log.PanicErrorf(err, "create topom with config file failed\n%s", config)
	}
	defer s.Close()

	log.Warnf("create topom with config\n%s", config)

	if s, ok := utils.Argument(d, "--pidfile"); ok {
		if pidfile, err := filepath.Abs(s); err != nil {
			log.WarnErrorf(err, "parse pidfile = '%s' failed", s)
		} else if err := ioutil.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
			log.WarnErrorf(err, "write pidfile = '%s' failed", pidfile)
		} else {
			defer func() {
				if err := os.Remove(pidfile); err != nil {
					log.WarnErrorf(err, "remove pidfile = '%s' failed", pidfile)
				}
			}()
			log.Warnf("option --pidfile = %s", pidfile)
		}
	}

	go func() {
		defer s.Close()
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM)

		sig := <-c
		log.Warnf("[%p] dashboard receive signal = '%v'", s, sig)
	}()

	for i := 0; !s.IsClosed() && !s.IsOnline(); i++ {
		if err := s.Start(true); err != nil {
			if i <= 15 {
				log.Warnf("[%p] dashboard online failed [%d]", s, i)
			} else {
				log.Panicf("dashboard online failed, give up & abort :'(")
			}
			time.Sleep(time.Second * 2)
		}
	}

	log.Warnf("[%p] dashboard is working ...", s)

	for !s.IsClosed() {
		time.Sleep(time.Second)
	}

	log.Warnf("[%p] dashboard is exiting ...", s)
}
