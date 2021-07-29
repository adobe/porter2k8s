/*
Copyright 2018 Adobe. All rights reserved.
This file is licensed to you under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License. You may obtain a copy
of the License at http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under
the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR REPRESENTATIONS
OF ANY KIND, either express or implied. See the License for the specific language
governing permissions and limitations under the License.
*/

//Usage of <global options> :
//  -config-path string
//        Configuration root directory (default "/repo/.porter")
//  -config-type string
//        Configuration type, the only option is porter (default "porter")
//  -deployment-file string
//        Vault token (default "/repo/deployment.yaml")
//  -environment string
//        Environment of deployment.
//  -regions string
//        Regions of deployment.
//  -service-file string
//        Vault token (default "/repo/service.yaml")
//  -sha string
//        Deployment sha.
//  -vault-addr string
//        Vault server (default "https://vault.loc.adobe.net")
//  -vault-path string
//        Path in Vault (default "/")
//  -vault-namespace string
//        Vault namespace (default "")
//  -vault-token string
//        Vault token
//
// Example:
// sha=6b875614b51bfab2f8b794279bda1598d555ed4b porter2k8s --regions "us-east-1" --environment dev --config-path $(pwd)
// --vault-path secret/ethos/tenants/cloudtech_doc_cloud
package main

import (
	"os"

	"git.corp.adobe.com/EchoSign/porter2k8s/pkg/porter2k8s"
)

func main() {
	porter2k8s.Run(os.Args[1:])
}
