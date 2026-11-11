# Accounts

Taproot sends requests through the Claude and Codex subscription accounts you add on the dashboard's **Accounts** page. Read [Terms of service](terms-of-service.md) before you add one.

## Sign in with a provider

1. On the **Accounts** page, select **Add account**.
2. Under **Sign in with a provider**, select Claude or Codex, then select **Open Link** to open the provider's sign-in page.
3. Sign in. The provider then sends you to a `http://localhost:...` address, which does not load.
4. Copy the full address from the address bar, paste it into **Paste the address you land on**, and select **Submit Callback URL**.

## Claude limits and resets

A Claude account's quota card, and its **Manage** sheet under **Quota and resets**, show three limits:

- **5-hour**: the rolling session limit. "No session open" means the next request starts one.
- **Weekly · all models**: the weekly limit. The hatched part of its bar is what Fable used.
- **Fable**: Fable models count toward the weekly limit and may use up to half of it. The row shows how much of that allowance is left and how much more of the weekly limit Fable can use. At the cap, Fable stops, or runs on usage credits if you turned them on, and the other models keep working.

Below the limits, **Resets** lists the resets Anthropic gave the account, one row per kind, each with its own button:

- **Full reset** refills the 5-hour, weekly, and Fable limits.
- **Fable reset** gives back the weekly usage Fable spent this week and, while a 5-hour session is open, refills the 5-hour limit. With no Fable use this week it has nothing to give back, so its button is off.
- **5-hour reset** and **Weekly reset** refill that limit.
- **Other reset** is a kind taproot doesn't recognize. Its confirmation lists what Anthropic says it clears.

No reset moves the weekly reset day. When the account is at a limit, the reset that clears it comes first. When you hold two resets of a kind, its button uses the one that clears the fewest limits, then the one that expires first. Each button asks first and shows what will change. Only a clear answer from Anthropic settles a reset. When the answer is lost or unclear, the reset may have been spent, so the row offers **Retry the same claim** for 10 minutes, which resends the same request rather than spending another reset. **Details** lists every grant with its expiry.

## Upload an existing credential

To move a credential from another proxy, select **Add account**, then **Upload auth file**, and choose its JSON file.

Use each account with one proxy only. Claude rotates refresh tokens, so two proxies that refresh the same account sign each other out.

Taproot records each Codex token refresh in `auths/.codex-refresh/` before it sends it. Do not copy Codex credentials to another machine without that directory. A [backup](configuration.md#the-data-directory) includes it.
