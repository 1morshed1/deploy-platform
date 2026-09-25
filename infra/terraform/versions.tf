# Provider and Terraform version constraints.
#
# In a team setting, configure a remote backend (S3 + DynamoDB lock) so state is
# shared and locked. State contains secrets, so it must never live in Git — the
# .gitignore excludes *.tfstate. The backend block is left commented so a fresh
# `terraform init` works locally; uncomment and fill in for real use.
terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.30"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.14"
    }
  }

  # backend "s3" {
  #   bucket         = "my-tf-state"
  #   key            = "deploy-platform/terraform.tfstate"
  #   region         = "us-east-1"
  #   dynamodb_table = "my-tf-locks"
  #   encrypt        = true
  # }
}

provider "aws" {
  region = var.region
}
