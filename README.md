invitechan
==========

A slack app that enables multi-channel guests to join open channels freely.
Open channels are marked by inviting @invitechan bot to public channels.

Usage
-----

Use `/plzinviteme` shasl command.

> Hello! With me multi-channel guests can join open channels freely.
>
> **If you are a multi-channel guest:**
>
> Tell me:
>
> * “list” to list open channels
> * “join channel” to join one
> * “leave channel” to leave one
>
> **If you are a regular user:**
>
> Public channels where I’m in are marked open to guests.
>
> Invite me to channels so that guests can join them.

Installation and deployment
---------------------------

* Create a Slack app with:
  * Bot user
  * Bot events subscribed on `message.im` with URL `.../invitechan` *TBD
  * Slash command `/plzinviteme` with URL `.../command` *TBD
* Create a file `env.yaml` with:
  * SLACK_TOKEN_USER key with value "OAuth Access Token"
  * SLACK_TOKEN_BOT key with value "Bot User OAuth Access Token"
* Create a Google Cloud Platform project
* Then `make deploy PROJECT=<project name>`
