# WebDeviceCable desktop 0.2.7

Makes connection cards easier to read. A website's logo fills its tile as sent,
scaled to fit, with no tile colour or inset around it. Websites show as their
host, such as `example.com`, without `https://`; the full address stays under
Connection details. The account ID moves into Connection details and no longer
appears on cards, invitation rows or connection requests.

Websites can pass an optional account label, such as the user's name or email,
when they connect. WDC shows it after the website, for example
`example.com · Jane Doe`, and lists it under Connection details. Without a
label, only the website shows. The label is saved when the owner approves the
connection, and connecting again saves the current one. It is display text only:
access is still checked against the account ID.

`@webdevicecable/sdk` and `@webdevicecable/relay` 0.2.7 add `userLabel` to the
connection review options of `requestPairing`, installed-app launch and
`RelayClient.enroll`. The relay accepts and shows the label and advertises
`userLabelVersion: 1`. The label is one line of up to 200 characters; anything
else is rejected with `invalid_user_label`.

Existing connections, older SDK packages and relay deployments remain
compatible. The 0.2.7 SDK leaves the label out when an older desktop app or
relay does not support it, so connecting never fails because of it. No database
migration is needed. Community downloads retain manual updates, ad-hoc macOS app
signing and unsigned Windows installers.
