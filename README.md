# porter2k8s
Deploy projects on Kubernetes.

## Introduction
Porter2k8s parses a configuration and deploys to kubernetes.

## Goals
1. Versatility
    1. Environment variables and secrets are tied to a deployment.
    2. Multi-cluster rollouts.
    3. No intermediary secret storage.  This reduces the operational overhead, but requires deployments to take place within Adobenet.  Therefore they cannot run in Lambda.
4. Capitalize on the advantages of Kubernetes.
   1. Deployments will only take a few minutes, rather than hours.
   2. All microservices in the cluster are assumed to be part of the same application, reducing the need for increased security and compliance controls.
   3. Allow developers to define their services and take advantage of all that Kubernetes can do.
5. As Vanilla as possible
   1. A single set of YAML files should:
    1. Cover all environments from local to production.
    2. Be compatible with the `kubectl apply`.

## Tools
1. Kubernetes
2. Vault (Thycotic has no REST API)

## Method
1. Gather environment variables and secret references for a specific environment (ie dev, stage, prod).
2. Fetch secrets from Vault.
3. Create deployment specific configmap and secret object. Both are tagged with the deployment sha and name specified in the deployment.yaml.
4. Read kubernetes objects from files within the repository.
6. Modify the objects with cluster specific settings.
    1. Update non-deployment objects if required.
    1. Apply the kubernetes deployment, statefulset, or job with the following amendments.
        1. Reference the previously created configmap and secret.
        2. Set the sha passed to the command as the docker image tag.
6. Monitor the status of the objects as they are created.

## Assumptions
1. The region name, for example "us-east-1" or "va7", matches the following:
    1. A context in the kubeconfig, or in `cluster-config.yaml` (see below).  If cluster-config.yaml does not exist, the kubeconfig defaults to `~/.kube/config` but can be set by the KUBECONFIG env variable.
    2. A directory within the `operation_data/<environment>` directory, which contains a `secret.yaml` file.
2. Kubernetes objects are kept in files within one or more of the following file names: service.yaml, deployment.yaml, istio.yaml, ingress.yaml, servicecatalog-aws, servicecatalog-azure, ethos-logging-sidecar. With the exception of the service catalog objects, The objects within these files do not need to correlate to the actual objects within them. Additionally, environment specific files will take precedence. For example if `deployment-dev.yaml` exists when deploying to the `dev` environment, then `deployment.yaml` will be ignored. These files may exist in $CONFIG_PATH or $CONFIG_PATH/k8s directory. Files in $CONFIG_PATH will be ignored if $CONFIG_PATH/k8s exists.
3. The docker image specified in the deployment.yaml:
    1. Matches the "name" specified in the deployment metadata.
    2. Has been built and exists within its repository with the sha passed to Porter2k8s in the command line argument or the SHA env variable.

## Kubeconfig
Porter2k8s can deploy using multiple kubeconfig files stored in Vault. This feature is enabled if `cluster-config.yaml` is found in the root of the $CONFIG_PATH.
1. The file `cluster-config.yaml` is of the following form, with region name matching a directory within the porter configuration (see assumptions):
```
clusters:
  - path: "path/to/kubeconfig/in/vault"
    context: "region name"
```
2. In Vault, the key must be `config` and the value the base64 encoded kubeconfig file.  For example:
```
vault write secret/k8s/test/us-east-1/kubeconfig-201809261820 config=@config-ue1_encoded
```

## Cluster specific settings
In order to have service.yaml, ingress.yaml (or istio.yaml), and deployment.yaml work for all environments and be `kubectl apply` compatible, porter28ks will automatically substitute certain values.  These substitutions will be based on a configmap named porter2k8s stored in the target kubernetes cluster in the same namespace as the service. The following key names are supported.
 * DOMAIN: All domain names in the ingress.yaml file will be changed to this domain. This may need to be refined further if multiple domains need to be supported.
 * REGISTRY: The registry of the image in the deployment.yaml will be changed to this value.
 * ISTIO: (true|false) Indicates whether istio objects should be created rather than ingress.
 * GATEWAYS: Name of Istio Gateway that should be used by the Virtual Service. Multiple space delimited gateways are allowed.
 * HPAMIN: Minimum value for horizontal pod autoscalers minimum allowed replicas.
 * HPAMINMAX: Maximum value for horizontal pod autoscalers minimum allowed replicas.
 * SIDECAR: Whole pods can be specified within the "SIDECAR" cluster setting.  The pod will be injected into the pod containing object (ex Statefulset, Deployment, Job).
 * CLOUD: (aws|azure) Identifies the cloud provider. This is only required for Open Service Broker service instances.
 * CLOUD_LOCATION: The cloud provider region. This can be required for Open Service Broker service instances.

In addition to specific keys, porter2k8s will replace Go template variables in the deployment spec annotations with values from the porter2k8s configmap. Sprig functions are also supported. http://masterminds.github.io/sprig/
```
arn:aws:iam::{{.account}}:role/myapp-{{.environment}}-{{.region}} ->
arn:aws:iam::9999999999:role/myapp-prod-us-east-2

{{ .support_subnets | splitList \",\" }} ->
[subnet-1932 subnet-9876]
```

## Annotations

### porter2k8s/service-name
Proteus2k8s is designed for multiple separate microservices to deploy to the same namespace. One of the perils of this model is microservices may overwrite the K8s objects of others. To help prevent that, porter2k8s will attempt to identify the name of the microservice and ensure all objects names contain that name. This microservice name can be specified explicitly with the annotation `porter2k8s/service-name` on the pod containing object (deployment, job, statefulset, etc). If that annotation is not present, it will use the name of the service object. If there is no service object, it will use the name of the pod containing object.

## Environment Files

Environment files specify secrets and environment values. There are two categories of entries _container_ and _porter2k8s_

### Container Entries
Secrets and environment variables that should be injected into pods.
#### Environment Variables
Environment variables are be specified directly.
```
---
base_path: /test_base
type: container
vars:

  - name: DESIRED_ENV_VARIABLE
    value: value of DESIRED_ENV_VARIABLE
```
#### Secrets
Secrets can have a number of sources.
* Vault secrets are specified with a path and a key. The path is the concatenation of the *vault_path* arg, the *base_path* specified for the yaml document within the environment file, and the *path* specified for the entry.
```
---
base_path: /vault_base_path
type: container
vars:
  - path: vault_path
    name: VAULT_SECRET
    key: vault_key
```

* Values from Kubernetes secrets can also be requested. The name of the secret must contain the _service name_ as defined in the Annotations section. The *base_path* is ignored.
```
---
base_path: /vault_base_path
type: container
vars:
  - source: kubernetes
    name: FROM_K8S
    k8s_secret: k8s-secret
    key: k8s_key
```
* Values from Kubernetes objects may be requested directly. Proteus2k8s will find the value at the specified object path, create a secret with that value, and mount the secret within the pod object. The path form conforms to jq (https://stedolan.github.io/jq/manual/). The object must be created by porter2k8s directly. Once again, the *base_path* is ignored.
```
---
base_path: /vault_base_path
type: container
vars:
  - source: object
    name: FROM_K8S_OBJECT
    path: .status.nodeGroups[0].primaryEndpoint.address
    k8s_object:
        name: my_elasticache_redis
        kind: ReplicationGroup
```

## Usage
```
$ porter2k8s --help
Usage of <global options> :
  -config-path string
        Configuration root directory. Should include the '.porter' or 'environment' directory. Kubernetes object yaml files may be in the directory or in a subdirectory named 'k8s'. (default "/repo")
  -config-type string
        Configuration type, simple or porter. (default "porter")
  -environment string
        Environment of deployment.
  -log-dir string
        Directory to write pod logs into. (must already exist) (default "logs")
  -log-mode string
        Pod log streaming mode. One of 'inline' (print to porter2k8s log), 'single' (single region to stdout), 'file' (write to filesystem, see log-dir option), 'none' (disable log streaming) (default "inline")
  -max-cm int
        Maximum number of configmaps and secret objects to keep per app. (default 5)
  -namespace string
        Kubernetes namespace. (default "default")
  -regions string
        Regions of deployment.
  -sha string
        Deployment sha.
  -v    Verbose log output.
  -vault-addr string
        Vault server. (default "https://vault.loc.adobe.net")
  -vault-path string
        Path in Vault. (default "/")
  -vault-token string
        Vault token.
  -wait int
        Extra time to wait for deployment to complete in seconds. (default 180)
```

## Examples
```
$ porter2k8s --sha f69a8e348ca46e738c00027aba814feb56195819 --regions "va7 us-east-1" --environment dev --config-path $(pwd) --vault-path secret/ethos/tenants/cloudtech_doc_cloud
```
or
```
$ docker run -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN -e VAULT_TOKEN -e VAULT_ADDR -e REGIONS -e ENVIRONMENT -e sha -e VAULT_PATH -v $HOME/.kube/:/root/.kube:z -v $(pwd)/:/repo:z docker-dc-micro-release.dr.corp.adobe.com/porter2k8s:1.0
```

## Example File structure
```
k8s
├── deployment.yaml
├── ingress.yaml
├── service.yaml
├── istio.yaml
├── ingress-prod.yaml
|── deployment-dev.yaml
└── servicecatalog-azure.yaml
environment
├── aws-environment.yaml
├── azure-environment.yaml
├── base-environment.yaml
├── dev
│   ├── environment.yaml
│   ├── eu-central-1
│   │   └── environment.yaml
│   ├── nld2
│   │   └── environment.yaml
│   └── us-east-1
│       └── environment.yaml
├── prod
│   ├── ap-northeast-1
│   │   └── environment.yaml
│   ├── ap-south-1
│   │   └── environment.yaml
│   ├── ap-southeast-2
│   │   └── environment.yaml
│   ├── environment.yaml
│   ├── eu-central-1
│   │   └── environment.yaml
│   ├── eu-west-1
│   │   └── environment.yaml
│   ├── nld2
│   │   └── environment.yaml
│   ├── us-east-1
│   │   └── environment.yaml
│   ├── us-west-2
│   │   └── environment.yaml
│   └── va7
│       └── environment.yaml
└── stage
    ├── ap-northeast-1
    │   └── environment.yaml
    ├── ap-south-1
    │   └── environment.yaml
    ├── ap-southeast-2
    │   └── environment.yaml
    ├── environment.yaml
    ├── eu-central-1
    │   └── environment.yaml
    ├── eu-west-1
    │   └── environment.yaml
    ├── nld2
    │   └── environment.yaml
    ├── us-east-1
    │   └── environment.yaml
    ├── us-west-2
    │   └── environment.yaml
    └── va7
        └── environment.yaml
```
