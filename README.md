# RRD Terminal Grapher

[termui](https://github.com/gizak/termui) based [rrd files](http://oss.oetiker.ch/rrdtool/index.en.html) grapher

## Install

    go get github.com/valobanov/rrdtg
    go build 
	
## Usage

    ./rrdtg [file list]
File list is ./*.rrd by default. For directories, all files added to the list (non-recursively).

