---
title: Ludus CLI Issues
---

If you encounter errors while using the Ludus CLI, the `--verbose` flag will print the full details of the request and response.
This data includes all the configuration file, environmental variables, and CLI arguments that are read and processed.
API keys are redacted after the `.` which shows the userID but nothing else. These command outputs are safe to share in issues or other documents.
The output will leak your username in the path to the configuration file if no configuration file is specified on the command line.

```plain
$ ludus users list all --verbose
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:101 Using config file: /Users/user/.config/ludus/config.yml
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:105 --- Configuration from cli and read from file ---
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:107 	url = https://10.98.108.227:8080
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:107 	proxy =
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:107 	verify = %!s(bool=false)
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:107 	user =
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:107 	verbose = %!s(bool=true)
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:107 	json = %!s(bool=false)
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:116 ---
[DEBUG] 2024/01/26 15:49:09 ludus/cmd.initConfig:root.go:130 Got API key: CI.***REDACTED***
[DEBUG] 2024/01/26 15:49:09 ludus/rest.InitClient:restapi.go:46 Endpoint URL:  https://10.98.108.227:8080
[DEBUG] 2024/01/26 15:49:09 ludus/rest.InitClient:restapi.go:56 Endpoint SSL Verify:  false
⠹ Waiting for server...2024/01/26 15:49:10.463798 DEBUG RESTY
==============================================================================
~~~ REQUEST ~~~
GET  /user/all  HTTP/1.1
HOST   : 10.98.108.227:8080
HEADERS:
	User-Agent: ludus-client/v1.0.0+4d9a395
	X-Api-Key: CI.***REDACTED***
BODY   :
***** NO CONTENT *****
------------------------------------------------------------------------------
~~~ RESPONSE ~~~
STATUS       : 200 OK
PROTO        : HTTP/2.0
RECEIVED AT  : 2024-01-26T15:49:10.463713-05:00
TIME DURATION: 1.281276292s
HEADERS      :
	Content-Length: 372
	Content-Type: application/json; charset=utf-8
	Date: Fri, 26 Jan 2024 20:49:10 GMT
BODY         :
[
   {
      "name": "root",
      "userID": "ROOT",
      "dateCreated": "2024-01-19T22:28:35.31750249Z",
      "dateLastActive": "2024-01-26T00:46:35.139188364Z",
      "isAdmin": true,
      "proxmoxUsername": "root"
   },
   {
      "name": "Continuous Integration",
      "userID": "CI",
      "dateCreated": "2024-01-19T22:30:04.303726015Z",
      "dateLastActive": "2024-01-26T20:49:10.498418274Z",
      "isAdmin": true,
      "proxmoxUsername": "continuous-integration"
   }
]
==============================================================================
+------------------------+--------+------------------+------------------+-------+
|          NAME          | USERID |     CREATED      |   LAST ACTIVE    | ADMIN |
+------------------------+--------+------------------+------------------+-------+
| root                   | ROOT   | 2024-01-19 17:28 | 2024-01-25 19:46 | true  |
| Continuous Integration | CI     | 2024-01-19 17:30 | 2024-01-26 15:49 | true  |
+------------------------+--------+------------------+------------------+-------+
```