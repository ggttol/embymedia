---
name: emby-user-policy
description: Use for listing Emby users, reading policies, creating or deleting users, changing policy limits, settings, and product credential rotation.
---

# User policy and settings

User and configuration reads use `embymedia_user` and `embymedia_config`. Never request or repeat a secret in chat.

Create `user.create`, `user.policy_update`, `user.delete`, or `config.update` plans for ordinary writes. Credential changes use `config.credential_rotate`; the dedicated card stages the value outside tool and Session history. CloudDrive webhook rotation generates its own value and completes only after sender restart and a real canary.

Verify user visibility/policy or credential status after execution. Rejection, expiry, restart, validation failure, or canary failure must leave the formal credential unchanged and remove pending state.
