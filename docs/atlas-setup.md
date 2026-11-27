# Sync across machines with MongoDB Atlas

Awesome Reminder can keep your items, their images and your reminder defaults in step across your computers. It uses a MongoDB Atlas cluster that you own; there is no account with this app and nothing goes through anyone else's server. Sync is off until you paste a connection string in Settings. Without one the app behaves exactly as before and never touches the network.

The free **M0** tier is enough.

## 1. Create a free cluster

1. Sign in at [cloud.mongodb.com](https://cloud.mongodb.com) and create a project (or use an existing one).
2. **Create a cluster**, pick the free **M0** tier and a region near you.

## 2. Create a database user for this app

The cluster may host other projects, so give the app its own user that can only touch its own database.

1. **Database Access → Add New Database User**, authentication method **Password**.
2. Enter a user name (for example `awesome-reminder`) and click **Autogenerate Secure Password**. Copy the password now; Atlas shows it once. Use a long generated password, because step 3 may open the cluster to every network.
3. Under **Database User Privileges** open **Specific Privileges** and add the built-in role **readWrite** on the database `awesome_reminder` (leave the collection empty). Remove any other role, such as the default "Read and write to any database".
4. Click **Add User**.

A broader user (`readWriteAnyDatabase`, or an Atlas admin) still syncs. The app checks the user's privileges on the first cycle after each start. If the user can reach other databases, Settings and the sync details in the list window show this warning: "This Atlas user can reach other databases on your cluster. Limit it to readWrite on awesome_reminder (see the setup guide)." It never blocks sync. The app only ever uses the database `awesome_reminder` (collections `items`, `attachments` and `settings`); a database name inside your connection string is ignored.

## 3. Let your computers reach the cluster

**Network Access → Add IP Address.** A laptop that moves between networks gets a new IP address often, so the simplest setting is **Allow access from anywhere** (`0.0.0.0/0`).

That is acceptable here because the cluster still demands the user name and the long random password from step 2, traffic is encrypted with TLS, and the user can only read and write `awesome_reminder`. If your machines have fixed addresses you can list only those instead. If the app says it can't reach the cluster, this list is the first thing to check.

## 4. Connect

1. **Database → Connect → Drivers** and copy the connection string. It looks like `mongodb+srv://awesome-reminder:<password>@cluster0.xxxxx.mongodb.net/?retryWrites=true&w=majority`.
2. Replace `<password>` with your password. If the password has special characters (`@ : / ? # %`), percent-encode them; an Atlas-generated password is safe as it is.
3. Open **Settings** in Awesome Reminder (tray menu or the gear), find **Sync across machines**, paste the string and click **Connect**.

The app validates the string, contacts the cluster and starts the first sync in the background. The list window then shows a sync pill and the tray icon a status dot.

Where the string is kept: in your operating system's keychain (Windows Credential Manager, macOS Keychain, Linux Secret Service). Where there is no keychain, it goes to a file instead, encrypted with AES-256-GCM under a random key in `sync.key` in the app's data folder. That keeps a copied database from leaking the string, but anyone who can read your whole user account can decrypt it; Settings says which of the two is in use. The string is never written to the log, and the app shows only the cluster's host name.

If the connection fails, Settings explains why: for example "Atlas rejected the username or password." for a wrong user or password, "This database user can't read and write awesome_reminder." for a missing role, or "Can't reach your Atlas cluster. It may be paused (resume it in Atlas), or this network's IP isn't on the Atlas access list."

## 5. Restore on a new machine

Install the app, open Settings, paste the same connection string and click **Connect**. The first sync merges both sides silently, so a fresh machine simply downloads everything: items, descriptions, images and the reminder defaults. Items that are already overdue come back as they were and do not pop up a "missed" card each; only an alert that fell due within the last two minutes still shows for a restored item.

If both machines already hold items, you get the union of both. When the same item was edited on both sides, the newest edit wins (see "Clocks").

## What syncs and what stays on each computer

Syncs:

- To-dos and reminders: text, tags, due time, repeat, priority, description, done and snoozed state, and deletions.
- The images in descriptions (at most 600 KB each, because the app shrinks them when you add them).
- The reminder defaults in Settings: alert lead times, the default time, and the day and time used for "next week".

Stays local on each computer:

- The global hotkey, the `todo.md` export path, "Start at login" and "Show pending count".
- The record of which alerts have already popped up. Each running computer still shows its own pop-ups. Marking an item done, snoozing or deleting it on one computer reaches the others through sync, so the card on another computer goes away once it syncs.
- The connection string, and the list of changes still waiting to be sent.

Deleted items stay in Atlas as small tombstones so that a deletion reaches every computer. Nothing is purged.

## How often it syncs

A local change is sent about 2 seconds later. Every 30 seconds the app also checks Atlas for changes from your other computers, so two running machines normally agree within about 30 seconds. **Sync now** (in the list's sync details or in Settings) runs a cycle at once.

With no network the pill reads "Offline" and the app retries with growing pauses, up to every 5 minutes. After an error such as a wrong password it retries every 5 minutes. Capturing, the list and alerts never wait for the network. Changes made offline are kept and sent when the connection is back; rows that are not sent yet carry a small "not synced yet" marker.

## Clocks

The newest edit wins, judged by the time the edit was made, so each computer's clock matters. If two computers edit the same item, the later clock time wins; if the times are identical, both sides pick the same winner. Keep automatic time on. If the app finds that this computer's clock is more than 2 minutes away from Atlas, Settings and the sync details say so; fix the clock to avoid losing edits.

## Free clusters pause

Atlas pauses an M0 cluster after a long time without use. The app then can't reach it, and the pill reads "Sync error" with the message about a paused cluster or the access list. Open the cluster in Atlas and click **Resume**; the next retry connects again, or press **Sync now**. Nothing is lost while it is paused, since your changes wait locally.

## Storage

M0 includes 512 MB. Text is tiny; images use the space, at most 600 KB each, so there is room for hundreds of them. Settings shows "Images in Atlas: N MB of 512 MB". If the cluster runs out of space, Atlas refuses writes and the app shows the error it gets back.

## Disconnect

**Settings → Sync across machines → Disconnect**. The app stops syncing and forgets the connection string. Everything on this computer stays, and Atlas keeps its copy. Connecting again to the same cluster resumes where it stopped; connecting to a different cluster uploads everything from this computer and downloads what that cluster holds. To remove the data from Atlas, delete the `awesome_reminder` database there, and delete the database user if you no longer need it.
