#!/usr/bin/env bash
# Rolls a built image tag out to one environment:
#   1. registers new revisions of every task definition with the new image,
#   2. runs the migrate task and waits for it to succeed,
#   3. updates the services (ECS rolls them with the circuit breaker on).
#
# Usage: infra/scripts/deploy.sh <staging|prod> <image-tag>
# Needs: aws CLI v2, jq, terraform (to read the environment's outputs).
# Rollback: run it again with the previous tag (images are immutable).
# Migrations are forward-only: a rollback across a schema change needs the
# old code to tolerate the new schema (add columns, never rename in one step).
set -euo pipefail

env="${1:?environment (staging|prod)}"
tag="${2:?image tag}"
root="$(cd "$(dirname "$0")/../terraform/envs/${env}" && pwd)"

out="$(terraform -chdir="$root" output -json deploy)"
cluster="$(jq -r .cluster <<<"$out")"
subnets="$(jq -r '.subnets | join(",")' <<<"$out")"
sg="$(jq -r .security_group <<<"$out")"
# Without NAT gateways the tasks run in public subnets and need a public IP.
public_ip="$(jq -r '.public_ip // "DISABLED"' <<<"$out")"

# register_revision <family> <image-kind>: copy the latest revision with a new image.
register_revision() {
  local family="$1" kind="$2" current repo
  current="$(aws ecs describe-task-definition --task-definition "$family" --query taskDefinition)"
  repo="$(jq -r '.containerDefinitions[0].image | split(":")[0]' <<<"$current")"
  repo="${repo%/api}"; repo="${repo%/web}"
  jq --arg image "${repo}/${kind}:${tag}" '
    .containerDefinitions[0].image = $image
    | {family, taskRoleArn, executionRoleArn, networkMode, containerDefinitions,
       requiresCompatibilities, cpu, memory, runtimePlatform}' <<<"$current" >/tmp/taskdef.json
  aws ecs register-task-definition --cli-input-json file:///tmp/taskdef.json \
    --query taskDefinition.taskDefinitionArn --output text
}

echo "==> ${env}: deploying ${tag}"
migrate_family="$(jq -r .migrate_family <<<"$out")"
migrate_arn="$(register_revision "$migrate_family" api)"

echo "==> running migrations (${migrate_arn##*/})"
task="$(aws ecs run-task --cluster "$cluster" --task-definition "$migrate_arn" --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[${subnets}],securityGroups=[${sg}],assignPublicIp=${public_ip}}" \
  --started-by "deploy-${tag:0:20}" --query 'tasks[0].taskArn' --output text)"
aws ecs wait tasks-stopped --cluster "$cluster" --tasks "$task"
code="$(aws ecs describe-tasks --cluster "$cluster" --tasks "$task" --query 'tasks[0].containers[0].exitCode' --output text)"
if [[ "$code" != "0" ]]; then
  echo "migrations failed (exit ${code}); services were not touched. Logs: /ecs/${migrate_family}" >&2
  exit 1
fi

# The API side first, the web last: it calls the API.
for name in api worker outbox-relay web; do
  family="$(jq -r --arg n "$name" '.services[$n].family' <<<"$out")"
  service="$(jq -r --arg n "$name" '.services[$n].service' <<<"$out")"
  kind="$(jq -r --arg n "$name" '.services[$n].image' <<<"$out")"
  arn="$(register_revision "$family" "$kind")"
  echo "==> ${service} -> ${arn##*/}"
  aws ecs update-service --cluster "$cluster" --service "$service" --task-definition "$arn" --query service.serviceName --output text >/dev/null
done

echo "==> waiting for the services to become stable"
services="$(jq -r '[.services[].service] | join(" ")' <<<"$out")"
# shellcheck disable=SC2086
aws ecs wait services-stable --cluster "$cluster" --services $services
echo "==> ${env} is running ${tag}"
