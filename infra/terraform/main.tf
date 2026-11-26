# Infrastructure for the platform: VPC, EKS, ECR, Route53, and the nginx ingress
# controller. This is a documented STARTING POINT — it provisions the cloud pieces
# the spec calls for. Review costs before `terraform apply` (see the spec's cost
# section; a single t3.medium node is ~$30/mo, plus the EKS control plane ~$73/mo).
#
# We use the community terraform-aws-modules for VPC and EKS because they encode a
# lot of hard-won correctness (subnet tagging for load balancers, IAM, OIDC) that
# is easy to get wrong by hand.

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 2)
  tags = {
    Project   = "deploy-platform"
    ManagedBy = "terraform"
  }
}

# --- Networking -----------------------------------------------------------
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = "${var.cluster_name}-vpc"
  cidr = var.vpc_cidr
  azs  = local.azs

  private_subnets = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets  = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 4, i + 8)]

  enable_nat_gateway = true
  single_nat_gateway = true # cost-saving for dev; use one-per-AZ in prod

  # These tags let the AWS load balancer controller / ELBs discover subnets.
  public_subnet_tags  = { "kubernetes.io/role/elb" = "1" }
  private_subnet_tags = { "kubernetes.io/role/internal-elb" = "1" }

  tags = local.tags
}

# --- EKS cluster ----------------------------------------------------------
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.0"

  cluster_name    = var.cluster_name
  cluster_version = var.kubernetes_version

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  cluster_endpoint_public_access = true

  eks_managed_node_groups = {
    default = {
      instance_types = [var.node_instance_type]
      min_size       = 1
      max_size       = var.node_max_size
      desired_size   = var.node_desired_size
    }
  }

  # Grant the identity running Terraform admin on the cluster so it can install
  # the ingress controller below.
  enable_cluster_creator_admin_permissions = true

  tags = local.tags
}

# --- Container registry ---------------------------------------------------
# One ECR repository holds every tenant's images (tagged per deployment). A
# lifecycle policy deletes untagged images after 30 days to control storage cost.
resource "aws_ecr_repository" "tenants" {
  name                 = "tenants"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true # AWS-side CVE scan, complementary to the Trivy gate
  }

  tags = local.tags
}

resource "aws_ecr_lifecycle_policy" "tenants" {
  repository = aws_ecr_repository.tenants.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Expire untagged images after 30 days"
      selection = {
        tagStatus   = "untagged"
        countType   = "sinceImagePushed"
        countUnit   = "days"
        countNumber = 30
      }
      action = { type = "expire" }
    }]
  })
}

# --- DNS ------------------------------------------------------------------
# A hosted zone for the wildcard app domain. After apply, delegate this zone from
# your registrar (point the parent domain's NS records at this zone's name
# servers, output below), then create a wildcard record *.<platform_domain> ->
# the ingress load balancer (the LB hostname is known only after the ingress
# controller creates its Service, so that record is typically managed by
# external-dns rather than statically here).
resource "aws_route53_zone" "apps" {
  name = var.platform_domain
  tags = local.tags
}

# --- Kubernetes + Helm providers (configured from the EKS outputs) ---------
data "aws_eks_cluster_auth" "this" {
  name = module.eks.cluster_name
}

provider "kubernetes" {
  host                   = module.eks.cluster_endpoint
  cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
  token                  = data.aws_eks_cluster_auth.this.token
}

provider "helm" {
  kubernetes {
    host                   = module.eks.cluster_endpoint
    cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
    token                  = data.aws_eks_cluster_auth.this.token
  }
}

# --- Ingress controller ---------------------------------------------------
# nginx ingress, fronted by an AWS Network Load Balancer. The
# platform.example.com/ingress=true label on its namespace is what the tenant
# NetworkPolicies allow traffic from (see the operator's NetworkPolicyFor).
resource "helm_release" "ingress_nginx" {
  name             = "ingress-nginx"
  repository       = "https://kubernetes.github.io/ingress-nginx"
  chart            = "ingress-nginx"
  namespace        = "ingress-nginx"
  create_namespace = true
  version          = "4.11.2"

  set {
    name  = "controller.service.annotations.service\\.beta\\.kubernetes\\.io/aws-load-balancer-type"
    value = "nlb"
  }

  depends_on = [module.eks]
}

# Label the ingress namespace so tenant NetworkPolicies can allow traffic from it.
resource "kubernetes_labels" "ingress_ns" {
  api_version = "v1"
  kind        = "Namespace"
  metadata {
    name = "ingress-nginx"
  }
  labels = {
    "platform.example.com/ingress" = "true"
  }
  depends_on = [helm_release.ingress_nginx]
}
