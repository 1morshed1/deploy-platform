# Terraform — cloud infrastructure for deploy-platform

Provisions the AWS pieces the platform needs: a VPC, an EKS cluster, an ECR
repository (with a lifecycle policy), a Route53 hosted zone for the wildcard app
domain, and the nginx ingress controller.

> ⚠️ **This costs money.** An EKS control plane is ~$73/month and one `t3.medium`
> node ~$30/month, plus NAT gateway and load balancer charges. Run
> `terraform destroy` when you're done. Review `terraform plan` before every apply.

## Usage

```bash
cd infra/terraform
cp example.tfvars terraform.tfvars      # edit values (terraform.tfvars is gitignored)
terraform init
terraform plan  -var-file=terraform.tfvars
terraform apply -var-file=terraform.tfvars
```

After apply:

```bash
# 1. Point kubectl at the new cluster (command is in the outputs):
$(terraform output -raw configure_kubectl)

# 2. Set ECR_REGISTRY in deploy/k8s/10-config.yaml to:
terraform output -raw ecr_repository_url

# 3. Delegate DNS: at your domain registrar, point the parent domain's NS records
#    at the name servers Terraform created:
terraform output route53_name_servers

# 4. Deploy the platform onto the cluster (see docs/learning/07):
make kind-load   # or push images to ECR and reference them
make deploy-platform
```

## What is intentionally left out

- **external-dns** — to auto-create the `*.<domain>` record pointing at the ingress
  load balancer. Without it, add the wildcard record manually after the ingress
  Service gets its external hostname.
- **cert-manager** — install via Helm and create a `ClusterIssuer`, then set
  `CLUSTER_ISSUER` in the platform config to get automatic TLS.
- **IRSA for ECR** — production should give the worker's ServiceAccount an IAM role
  (IRSA) to push to ECR instead of a static `ecr-credentials` secret.

These are documented extension points, not oversights — see
`docs/learning/09-what-is-real-vs-simulated.md`.
