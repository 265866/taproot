# Terms of service

This page is a warning. It is not legal advice.

Taproot is for personal use of your own subscription accounts, at your own risk. It signs in to those accounts with OAuth, stores the tokens, and sends requests from every client on your tailnet through them. The providers' terms address this directly:

- Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) say: "You may not share your Account login information, Anthropic API key, or Account credentials with anyone else." They also forbid access "through automated or non-human means" except with an API key or where Anthropic expressly permits it.
- Anthropic's [Claude Code legal and compliance page](https://code.claude.com/docs/en/legal-and-compliance) says: "Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, Max, or Ultra plan credentials on behalf of their users." It also says that advertised plan limits "assume ordinary, individual usage".
- Anthropic's [Usage Policy](https://www.anthropic.com/legal/aup) lists "services that route requests through consumer subscriptions or misrepresent the product or client being used" under unauthorized access.
- OpenAI's [Terms of Use](https://openai.com/policies/terms-of-use/), effective January 1, 2026, say under "Registration and access": "You may not share your account credentials or make your account available to anyone else and are responsible for all activities that occur under your account." Under "Using our Services", one of the things you may not do is: "Automatically or programmatically extract data or Output". The terms do not mention Codex.

CLIProxyAPI, by default, presents clients other than Claude Code as the Claude Code CLI on Claude accounts. `upstream.claude.disable-claude-cloak-mode` in `config.yaml` turns that off.

A provider can suspend or close an account that breaks its terms. Use taproot only with accounts you own, for your own use. Read each provider's current terms before you add an account, and decide for yourself whether your use is allowed.
