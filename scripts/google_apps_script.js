/**
 * ═══════════════════════════════════════════════════════════════════
 * SPECTER - Autonomous Google Sheets Two-Tab Webhook Engine
 * ═══════════════════════════════════════════════════════════════════
 * 
 * Features:
 * 1. Automatically manages TWO tabs in a single Google Sheet:
 *    - Tab 1: "Contacts" (Engineering Leads & Technical Contributors)
 *    - Tab 2: "Jobs" (Direct One-Click Apply Backend & Remote Roles)
 * 2. Beautiful aesthetic header formatting (Navy theme, frozen top row).
 * 3. Automatic deduplication:
 *    - Contacts deduplicated by Email or GitHub handle.
 *    - Jobs deduplicated by Apply URL.
 * 4. Supports envelope requests ({ type: "leads"|"jobs", data: [...] })
 *    and direct array requests.
 * 
 * Instructions:
 * 1. Open your Google Sheet.
 * 2. Extensions -> Apps Script.
 * 3. Paste this entire code into Code.gs (replacing everything).
 * 4. Deploy -> New Deployment -> Select type: Web app.
 *    - Execute as: Me
 *    - Who has access: Anyone
 * 5. Copy the Webhook URL and paste into your .env file:
 *    SPECTER_SHEETS_WEBHOOK="https://script.google.com/macros/s/.../exec"
 */

function doPost(e) {
  var lock = LockService.getScriptLock();
  try {
    // Wait up to 30 seconds for concurrent write safety
    lock.waitLock(30000);
    
    var rawData = e.postData.contents;
    var parsed = JSON.parse(rawData);
    var ss = SpreadsheetApp.getActiveSpreadsheet();
    
    var type = "leads";
    var items = [];
    
    if (Array.isArray(parsed)) {
      items = parsed;
      if (items.length > 0 && items[0].apply_url !== undefined) {
        type = "jobs";
      } else {
        type = "leads";
      }
    } else if (parsed && typeof parsed === "object") {
      type = (parsed.type || parsed.target || "leads").toLowerCase();
      items = parsed.data || parsed.jobs || parsed.leads || [];
    }
    
    var count = 0;
    if (type === "jobs") {
      count = appendJobs(ss, items);
    } else {
      count = appendLeads(ss, items);
    }
    
    return ContentService
      .createTextOutput(JSON.stringify({ status: "success", type: type, appended: count }))
      .setMimeType(ContentService.MimeType.JSON);
      
  } catch (err) {
    return ContentService
      .createTextOutput(JSON.stringify({ status: "error", message: err.toString() }))
      .setMimeType(ContentService.MimeType.JSON);
  } finally {
    lock.releaseLock();
  }
}

function doGet(e) {
  return ContentService
    .createTextOutput(JSON.stringify({ status: "online", engine: "Specter Webhook 2.0" }))
    .setMimeType(ContentService.MimeType.JSON);
}

// ───────────────────────────────────────────────────────────────────
// Tab 1: Contacts / Engineering Leads
// ───────────────────────────────────────────────────────────────────
function appendLeads(ss, leads) {
  if (!leads || leads.length === 0) return 0;
  
  var sheetName = "Contacts";
  var sheet = ss.getSheetByName(sheetName);
  if (!sheet) {
    sheet = ss.insertSheet(sheetName);
  }
  
  var headers = [
    "Score", "Company", "Name", "Role", "Topic", "Email", "LinkedIn", "GitHub", "Repo", "Icebreaker", "Synced At"
  ];
  
  initSheetHeaders(sheet, headers, "#0F172A"); // Slate-900 Dark Header
  
  // Build lookup index of existing emails & github handles to prevent duplicate rows
  var existingMap = {};
  var lastRow = sheet.getLastRow();
  if (lastRow > 1) {
    var emailValues = sheet.getRange(2, 6, lastRow - 1, 1).getValues();
    var ghValues = sheet.getRange(2, 8, lastRow - 1, 1).getValues();
    for (var i = 0; i < emailValues.length; i++) {
      var em = (emailValues[i][0] || "").toString().toLowerCase().trim();
      var gh = (ghValues[i][0] || "").toString().toLowerCase().trim();
      if (em) existingMap[em] = true;
      if (gh) existingMap[gh] = true;
    }
  }
  
  var now = Utilities.formatDate(new Date(), "UTC", "yyyy-MM-dd HH:mm:ss");
  var newRows = [];
  
  for (var j = 0; j < leads.length; j++) {
    var l = leads[j];
    var emailKey = (l.email || "").toLowerCase().trim();
    var ghKey = (l.github || "").toLowerCase().trim();
    
    // Deduplication check
    if ((emailKey && existingMap[emailKey]) || (ghKey && existingMap[ghKey])) {
      continue;
    }
    
    newRows.push([
      l.score || 0,
      l.company || "",
      l.name || "",
      l.role || "",
      l.topic || "",
      l.email || "",
      l.linkedin || "",
      l.github || "",
      l.repo || "",
      l.icebreaker || "",
      now
    ]);
    
    if (emailKey) existingMap[emailKey] = true;
    if (ghKey) existingMap[ghKey] = true;
  }
  
  if (newRows.length > 0) {
    var startRow = sheet.getLastRow() + 1;
    sheet.getRange(startRow, 1, newRows.length, headers.length).setValues(newRows);
  }
  
  return newRows.length;
}

// ───────────────────────────────────────────────────────────────────
// Tab 2: Jobs / Direct One-Click Apply Roles
// ───────────────────────────────────────────────────────────────────
function appendJobs(ss, jobs) {
  if (!jobs || jobs.length === 0) return 0;
  
  var sheetName = "Jobs";
  var sheet = ss.getSheetByName(sheetName);
  if (!sheet) {
    sheet = ss.insertSheet(sheetName);
  }
  
  var headers = [
    "Company", "Role Title", "Location", "Workplace", "Remote Policy", "Global Remote?", "Contractor Friendly?", "Compensation", "Apply Link", "Discovered At"
  ];
  
  initSheetHeaders(sheet, headers, "#1E293B"); // Slate-800 Header
  
  // Build lookup index of existing apply URLs to prevent duplicate job rows
  var existingMap = {};
  var lastRow = sheet.getLastRow();
  if (lastRow > 1) {
    var applyValues = sheet.getRange(2, 9, lastRow - 1, 1).getValues();
    for (var i = 0; i < applyValues.length; i++) {
      var url = (applyValues[i][0] || "").toString().toLowerCase().trim();
      if (url) existingMap[url] = true;
    }
  }
  
  var newRows = [];
  for (var j = 0; j < jobs.length; j++) {
    var job = jobs[j];
    var urlKey = (job.apply_url || "").toLowerCase().trim();
    if (urlKey && existingMap[urlKey]) {
      continue;
    }
    
    newRows.push([
      job.company || "",
      job.title || "",
      job.location || "",
      job.workplace_type || "",
      job.remote_policy || "",
      job.global_remote ? "YES" : "NO",
      job.contractor_friendly ? "YES" : "NO",
      job.compensation || "",
      job.apply_url || "",
      job.discovered_at || Utilities.formatDate(new Date(), "UTC", "yyyy-MM-dd")
    ]);
    
    if (urlKey) existingMap[urlKey] = true;
  }
  
  if (newRows.length > 0) {
    var startRow = sheet.getLastRow() + 1;
    sheet.getRange(startRow, 1, newRows.length, headers.length).setValues(newRows);
  }
  
  return newRows.length;
}

// ───────────────────────────────────────────────────────────────────
// Header Styling & Sheet Formatting Helper
// ───────────────────────────────────────────────────────────────────
function initSheetHeaders(sheet, headers, headerColor) {
  if (sheet.getLastRow() === 0) {
    sheet.appendRow(headers);
    var range = sheet.getRange(1, 1, 1, headers.length);
    range.setBackground(headerColor);
    range.setFontColor("#FFFFFF");
    range.setFontWeight("bold");
    range.setFontFamily("Inter");
    range.setHorizontalAlignment("center");
    sheet.setFrozenRows(1);
    
    // Set friendly initial column widths
    for (var col = 1; col <= headers.length; col++) {
      sheet.setColumnWidth(col, 160);
    }
    sheet.setColumnWidth(10, 260); // Wider for Icebreaker or Apply Link
  }
}
