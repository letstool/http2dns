#!/bin/bash

curl -s -X POST http://localhost:8080/api/v1/dns \
      	-H 'Content-Type: application/json' \
      	-d '{
	      	"class":"IN",
	        "type":"A",
        	"record":"m.root-servers.net.",
	        "timeout":10,
        	"dnsservers":["1.1.1.1","8.8.8.8"]
      }' | jq

curl -s -X POST http://localhost:8080/api/v1/dns \
      	-H 'Content-Type: application/json' \
      	-d '{
	      	"class":"IN",
	        "type":"TXT",
        	"record":"google.com.",
	        "timeout":10,
        	"dnsservers":["1.1.1.1","8.8.8.8"]
      }' | jq

curl -s -X POST http://localhost:8080/api/v1/dns \
      	-H 'Content-Type: application/json' \
      	-d '{
		"record": "google.com",
		"classe": "IN",
		"type": "TXT",
		"dnsservers": [
			"1.1.1.1:53"
		],
		"timeout": 10
      }' | jq
