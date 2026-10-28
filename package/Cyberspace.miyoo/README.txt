CYBERSPACE.MIYOO — a cyberspace.online terminal for OnionOS (Miyoo Mini Plus)
===================================================================
Built by Claude, Anthropic's AI model, for snyderman3000.
v0.3.0 (formerly Dialup)

Cyberspace.miyoo is a client for Cyberspace (https://cyberspace.online), the
text-only social network. Read your feed, browse topics, open entries with
their threaded replies, check your alerts, and write entries and replies
with an on-screen keyboard. Send private messages (C-Mail) and chat in
rooms (cIRC), with live updates while you're in them. Three looks, like the
website: cyber (green), c64 and vt320 (amber).

Cyberspace.miyoo is an independent client, released with the blessing of
Cyberspace's developer. It isn't made by Cyberspace, so report problems to
github.com/snyderman3000/cyberspace.miyoo, not to them. It uses the public
API under its terms: one person driving it by hand - no bots, no scraping.

COMING FROM DIALUP?  Install this, open it once and you're still logged in:
  it copies App/Dialup/data over. Then delete the App/Dialup folder.

INSTALL
  1. Copy the "App" folder from this zip to the root of your SD card
     (merge it with the App folder that's already there).
  2. Turn Wi-Fi on and open Apps -> Cyberspace.miyoo.
  3. Log in with your Cyberspace email and password. The account must
     have a verified email (sign up on the website first).
     Next time it logs you in by itself. If Wi-Fi is still joining, it
     keeps trying for about 30 seconds first.

  Rather not type a password on the d-pad? Before step 2, open
  App/Cyberspace.miyoo/data/config.json on a computer (run the app
  once to create it, or make it yourself) and fill in:
       { "email": "you@example.com", "password": "your password" }
  The app logs in at launch and then ERASES the password from the file.
  Only a session token is kept (data/session.json); START on the feed
  logs out and deletes it.

CONTROLS
  LISTS     Up/Down move · Left/Right page · A open · L1/R1 tabs
            X new entry (on ALERTS: mark all read; on MAIL: new
            message) · Y refresh
            B back · SELECT theme · START log out · MENU quit
  ENTRY     Up/Down read (moves reply to reply) · L1/R1 page
            A reply to the highlighted entry/reply · X reply to the entry
            Y reload · B back
  KEYBOARD  D-pad move · A type · B delete · X space · Y shift
            L1/R1 move the cursor · L2 symbols · R2 new line
            START send/done · SELECT back (your draft is kept)
  NEW ENTRY A edit a field / toggle · START transmit · Y discard · B back
  CHAT      Up/Down scroll · L1/R1 page · A say something · Y who's
            here (rooms) · START reload · B back (leaves the room)
            MENU quit
  MAIL      Unread conversations come first. Messages arrive live.
  CIRC      Rooms you can join. Opening one shows who's here.

  Sending always asks first. Cyberspace allows 2 entries a minute
  (24 a day) and 3 replies a minute (48 a day).

LIVE UPDATES
  Open a conversation or room and new lines appear by themselves over a
  live connection. If the connection drops, it reconnects on its own
  (up to six tries) and shows "reconnecting" in the status line. Your
  own sends are never delayed by it. Leaving a room tells the server you
  have gone, so it drops off the "who's here" list.

FILES  (App/Cyberspace.miyoo/data/)
  config.json    theme, email (and the one-time password)
  session.json   your login token - delete it to log out
  drafts.json    unsent entry and replies
  cyberspace.log what went wrong, if something did

THANKS
  The cyber-tui project (github.com/ArmadilloBrillo/cyber-tui, MIT) for
  documenting the API. Fonts: VT323 (SIL OFL 1.1). See licenses/.
