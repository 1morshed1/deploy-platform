variable "region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "us-east-1"
}

variable "cluster_name" {
  description = "EKS cluster name."
  type        = string
  default     = "deploy-platform"
}

variable "kubernetes_version" {
  description = "EKS control-plane Kubernetes version."
  type        = string
  default     = "1.30"
}

variable "vpc_cidr" {
  description = "CIDR block for the platform VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "node_instance_type" {
  description = "EC2 instance type for the managed node group. t3.medium ~= $30/mo (see spec's cost notes)."
  type        = string
  default     = "t3.medium"
}

variable "node_desired_size" {
  description = "Desired number of worker nodes."
  type        = number
  default     = 1
}

variable "node_max_size" {
  description = "Maximum number of worker nodes."
  type        = number
  default     = 3
}

variable "platform_domain" {
  description = "Wildcard base domain for tenant apps (a Route53 hosted zone is created for it)."
  type        = string
  default     = "apps.example.com"
}
