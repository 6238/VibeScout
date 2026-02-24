package main

import (
    "bytes"
    "encoding/json"
    "net/http"
    tpl "html/template"
)

// Simple HTML page for data analysis with a dropdown for events and a Run Analysis button.
// This page is served at /data-analysis
func dataAnalysisPageHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    events, err := getEventsCached("2026")
    if err != nil {
        http.Error(w, "Could not load events", http.StatusInternalServerError)
        return
    }

    // Build HTML with Tailwind-like styling via CDN for consistency
    var buf bytes.Buffer
    buf.WriteString(`<!doctype html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Data Analysis</title><script src="https://cdn.tailwindcss.com"></script></head><body class="bg-[#F7F0E6] font-sans text-stone-800"><div class="min-h-screen flex items-center justify-center"><div class="max-w-4xl w-full bg-white/90 rounded-3xl p-6 shadow-2xl"><h1 class="text-3xl font-black mb-4">Data Analysis</h1>`)
    // Event dropdown
    buf.WriteString(`<div class="mb-4"><label class="block text-sm font-semibold mb-1">Select Event</label><select id="eventKey" class="w-full p-3 border rounded-md">`)
    for key, name := range events {
        // Note: keep order stable by iterating range; if needed, sort by name
        buf.WriteString(`<option value="`)
        buf.WriteString(tpl.HTMLEscapeString(key))
        buf.WriteString(`">`)
        buf.WriteString(tpl.HTMLEscapeString(name))
        buf.WriteString(`</option>`)
    }
    buf.WriteString(`</select></div> <button id="runBtn" class="bg-[#5D4037] text-white font-bold py-2 px-6 rounded-xl">Run Analysis</button>`)

    // Results area
    buf.WriteString(`<div id="results" class="mt-6"></div>`)
    // Script to fetch results and render simple visuals
    buf.WriteString(` <script>
            async function runAnalysis(){
                const sel = document.getElementById('eventKey');
                const eventKey = sel.value;
                const resp = await fetch('/api/analysis/run?event_key=' + encodeURIComponent(eventKey));
                if (!resp.ok) { alert('Failed to run analysis'); return; }
                const data = await resp.json();
                renderResults(data);
            }
            function renderResults(sum){
                const el = document.getElementById('results');
                el.innerHTML = '';
                // Stability score (show numeric value and a simple label)
                const stability = sum.stability;
                const gauge = `<div class="mb-4"><div class="text-lg font-bold mb-2">Stability Score</div><div id="stability-value" class="text-2xl font-extrabold"></div></div>`;
                el.innerHTML += gauge;
                // Show numeric stability value (0-100%) if available
                if (isFinite(stability)) {
                    document.getElementById('stability-value').innerText = Math.round((stability) * 100) + "%";
                } else {
                    document.getElementById('stability-value').innerText = "n/a";
                }
                // Simple text dump of team variabilities for now
                const variabilities = sum.variabilities || [];
                const pre = document.createElement('pre');
                pre.textContent = JSON.stringify(variabilities, null, 2);
                el.appendChild(pre);
            }
            document.getElementById('runBtn').addEventListener('click', runAnalysis);
        </script>`) // end script
    buf.WriteString(`</div></div></body></html>`)

    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Write(buf.Bytes())
}

// API endpoint to trigger analysis for a given event (returns AnalysisSummary JSON)
func analysisRunHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    eventKey := r.URL.Query().Get("event_key")
    if eventKey == "" {
        http.Error(w, "Missing event_key", http.StatusBadRequest)
        return
    }
    summary, err := analyzeEvent(eventKey)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(summary)
}
