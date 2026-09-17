# Multi-Tab Management and Tab Groups

Tether organizes automated tabs in a blue **Tether** tab group in the developer browser window.

---

## 1. Tab Group Rules

* **Isolation**: Tether never modifies tabs outside the group. Personal tabs remain untouched.
* **Child tab inheritance**: When a page in the group opens a popup or link, Chrome adds the child tab to the group automatically.
* **Adoption**: To add an external tab to the group, click the tab in the popup menu or run `tether switch <tabId>`.
---

## 2. Listing Open Tabs: `tether tabs`

`tether tabs` lists all open tabs inside the Tether tab group:

```bash
tether tabs
```

Output:
```text
Open Tabs (3):
   [1] "Search Engine"
     URL: https://example.com/search
     ID:  101
   [2] "API Documentation"
     URL: https://docs.example.com/api
     ID:  102
 * [3] "Operations Dashboard"
     URL: https://app.example.com/dashboard
     ID:  103
```

* `*`: Marks the active tab.
* `ID`: The tab identifier for `--tab <id>`.

---

## 3. Switching Active Focus: `tether switch`

### `tether switch <tabId>`
Switches active focus to the specified tab:

```bash
tether switch 103
```

Output:
```text
Switched active tab to 103
```

Commands without `--tab` target this tab.
---

## 4. Direct Tab Targeting: `--tab <id>`

To target a tab without changing focus, pass `--tab <id>` or `-t <id>`:
```bash
# Take a snapshot of tab 2 while keeping tab 3 active
tether snapshot --tab 102 -i

# Click a button on a specific tab
tether click --tab 102 @e5

# Evaluate JavaScript in an unselected tab
tether eval --tab 102 "document.title"

# Capture a screenshot of a background tab
tether screenshot --tab 102 /tmp/tab2.png
```

---

## 5. Multi-Tab Workflow Example

To coordinate actions across two tabs:
# 1. Discover all open tabs
tether tabs

# 2. Switch to the verification email tab
tether switch 101
tether snapshot -i

# 3. Read verification code
tether eval "document.querySelector('.verification-code').innerText"

# 4. Switch back to the application tab
tether switch 103
tether fill @e4 "123456"
tether press Enter
```
