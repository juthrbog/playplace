#!/usr/bin/env bash
# Seed LocalStack's Identity Center with an instance, the PlaygroundOwner
# permission set, and the three Dex users, so owner validation and access
# grants can be exercised locally. Prints the export to use.
set -euo pipefail
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=us-east-1
A=(aws --endpoint-url "${AWS_ENDPOINT_URL:-http://localhost:4566}")

INST=$("${A[@]}" sso-admin list-instances --query 'Instances[0].InstanceArn' --output text 2>/dev/null || true)
if [ -z "$INST" ] || [ "$INST" = "None" ]; then
  "${A[@]}" sso-admin create-instance --name playplace-local >/dev/null
  INST=$("${A[@]}" sso-admin list-instances --query 'Instances[0].InstanceArn' --output text)
fi
STORE=$("${A[@]}" sso-admin list-instances --query 'Instances[0].IdentityStoreId' --output text)

PS=$("${A[@]}" sso-admin list-permission-sets --instance-arn "$INST" --query 'PermissionSets[0]' --output text)
if [ -z "$PS" ] || [ "$PS" = "None" ]; then
  PS=$("${A[@]}" sso-admin create-permission-set --instance-arn "$INST" --name PlaygroundOwner \
        --query 'PermissionSet.PermissionSetArn' --output text)
fi

for u in admin lead dev; do
  "${A[@]}" identitystore create-user --identity-store-id "$STORE" --user-name "$u" --display-name "$u" \
    --name "FamilyName=Example,GivenName=$u" --emails "Value=$u@example.com,Type=work,Primary=true" >/dev/null 2>&1 || true
done

echo "identity center: $INST"
echo "users: admin@example.com lead@example.com dev@example.com"
echo
echo "LocalStack cannot describe permission sets by name, so pass the ARN:"
echo "  export PLAYPLACE_PERMISSION_SET=$PS"
