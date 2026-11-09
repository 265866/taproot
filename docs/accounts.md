# Accounts

Taproot sends requests through the Claude and Codex subscription accounts you add on the dashboard's **Accounts** page. Read [Terms of service](terms-of-service.md) before you add one.

## Sign in with a provider

1. On the **Accounts** page, select **Add account**.
2. Under **Sign in with a provider**, select Claude or Codex, then select **Open Link** to open the provider's sign-in page.
3. Sign in. The provider then sends you to a `http://localhost:...` address, which does not load.
4. Copy the full address from the address bar, paste it into **Callback URL**, and select **Submit Callback URL**.

## Upload an existing credential

To move a credential from another proxy, select **Add account**, then **Upload auth file**, and choose its JSON file.

Use each account with one proxy only. Claude rotates refresh tokens, so two proxies that refresh the same account sign each other out.

Taproot records each Codex token refresh in `auths/.codex-refresh/` before it sends it. Do not copy Codex credentials to another machine without that directory. A [backup](configuration.md#the-data-directory) includes it.
