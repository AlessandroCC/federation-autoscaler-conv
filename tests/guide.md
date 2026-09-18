# Guida ai test comparativi

Come funzionano i test in `tests/`, come lanciarli, cosa toccare per cambiare
esperimento e come capire se una run è valida.

Il punto di partenza: **è tutto automatizzato**. Non devi creare cluster, generare
certificati, deployare componenti né pulire niente a mano. Un solo comando fa
l'intero giro, e nel caso normale l'unica cosa che tocchi è un file YAML in
`tests/configs/`.

---

## 1. Cosa c'è nella cartella

| Percorso | Cos'è |
|---|---|
| `comparative-eco/` | Harness del test Random vs Eco (carbon intensity) |
| `comparative-latency/` | Harness del test Random vs Latency (RTT) |
| `consumerchoice/` | Validazione end-to-end di ConsumerChoice: la scelta del provider la fa un LLM locale (Ollama). Ha un suo README |
| `configs/` | I file YAML che descrivono gli esperimenti — **è qui che lavori** |
| `testlib/` | Libreria condivisa: orchestrazione, deploy, client, scrittura CSV |
| `scripts/` | Script Python di analisi e verifica dei risultati |
| `mock-eco-test/`, `mock-geo-test/` | Servizi finti che rispondono con carbon intensity e geolocalizzazione; li deploya l'harness, non li lanci tu |
| `scalability/` | Test di carico sull'API del Broker, indipendente da tutto il resto (ha un suo README) |

---

## 2. Come funziona un test comparativo

Entrambi gli harness eseguono **due fasi consecutive sulla stessa federazione**:

- **Phase A — Random**: la baseline. Il broker sceglie un provider a caso.
- **Phase B — Eco / Latency**: la policy sotto esame.

L'idea è un **esperimento appaiato**: le due fasi devono vedere lo *stesso*
ambiente, così che l'unica differenza tra loro sia la policy. Non è un dettaglio
metodologico astratto — è ciò che rende lecito sovrapporre i due grafici e dire
"a parità di condizioni, Eco ha fatto meglio".

Per ottenerlo l'harness fa due cose:

1. **Replay della sequenza.** Il generatore di condizioni ambientali (carbon
   intensity nell'eco, ritardi `tc netem` nel latency) viene riavviato all'inizio
   di ogni fase con lo **stesso seme casuale**, derivato dal RunID. La Phase B
   quindi ripropone esattamente i valori che aveva prodotto la Phase A, negli
   stessi istanti relativi all'inizio della propria fase.

2. **Attese simmetriche.** Ogni pausa prima del campionamento è identica nelle due
   fasi. Se una fase aspettasse anche solo qualche secondo in più, i suoi campioni
   cadrebbero su un punto diverso della sequenza replicata e l'allineamento
   salterebbe.

### Quello che l'harness genera non è quello che i consumer vedono

È il concetto che serve capire per scegliere bene i valori di config.

Quando l'harness scrive un nuovo valore di carbonio, quel valore **non arriva
subito** al consumer. Deve attraversare due stadi:

1. la cache del provider deve scadere → fino a `ecoCacheTTL`
2. il provider deve ripubblicare il proprio annuncio → fino a **30 s**, cablati
   nel codice dell'agent, non configurabili

Chiamiamo **ritardo di osservazione** la somma dei due. Ogni provider ha un
proprio sfasamento all'interno di quella finestra, e quello sfasamento è diverso
tra Phase A e Phase B.

**La regola pratica: il ritardo di osservazione deve essere molto più piccolo
dell'intervallo con cui l'ambiente cambia.** Altrimenti, quando osservi un
valore, non sai nemmeno a quale "giro" appartenga — e le due fasi non risultano
sovrapponibili, anche se la sequenza generata era identica.

| Configurazione | Ritardo | Intervallo | Rapporto | Esito misurato |
|---|---|---|---|---|
| `35s` / `35s` (vecchia) | 65 s | 35 s | 1.86 | 15.5 % di corrispondenza — inutilizzabile |
| `2m` / `5s` (attuale) | 35 s | 120 s | 0.29 | atteso ~90 % |

Sul lato latency la catena è più corta — il consumer sonda direttamente il
provider, senza passare dagli annunci — quindi il ritardo è solo la cache del
prober (15 s, anch'essa cablata). Con `latencyRefreshInterval: 2m` il rapporto è
0.12.

L'harness **controlla questi rapporti all'avvio** e stampa un warning se il
ritardo supera metà dell'intervallo. Se lo vedi, la run non produrrà grafici
sovrapponibili: fermala e sistema la config.

---

## 3. Lanciare un test

### Prerequisiti

`docker` (in esecuzione), `kind` e `go` sul `PATH`. L'harness li verifica e si
ferma subito se manca qualcosa.

### Comando

```bash
go run ./tests/comparative-eco/     --config tests/configs/eco-test.yaml
go run ./tests/comparative-latency/ --config tests/configs/latency-test.yaml
```

Tutto qui. Da questo momento l'harness fa da solo, nell'ordine:

```
PREREQUISITES → BUILD IMAGES → RETAG → PRELOAD LIQO+UDPECHO → CREATE CLUSTERS
→ LOAD IMAGES → DEPLOY COMPONENTS → CAP PROVIDER CAPACITY → WAIT FOR READINESS
→ PHASE A → TRANSITION → PHASE B → EXPERIMENT CLEANUP → CLEANUP → DONE
```

Crea un cluster Kind per il broker, uno per ogni consumer e uno per ogni
provider, genera la PKI, deploya broker/agent/mock, esegue le due fasi, scrive i
risultati e distrugge tutto.

### Flag disponibili

| Flag | Effetto | Quando serve |
|---|---|---|
| `--config` | Percorso del file YAML | Sempre |
| `--skip-build` | Non ricostruisce le immagini Docker | Dalla seconda run in poi, se non hai toccato il codice Go — risparmia parecchi minuti |
| `--keep-clusters` | Non distrugge i cluster a fine run | Debug: puoi entrare con `kubectl` a guardare cosa è successo |
| `--run-id` | Forza il RunID invece di generarlo | Rarissimo. **Attenzione**: il RunID è il seme del replay, quindi due run con lo stesso `--run-id` vedono la stessa identica sequenza di condizioni |

> **Attenzione a `--skip-build` dopo un aggiornamento del codice.** I manifest vengono
> sempre presi dal repository, le immagini no. Il manifest del consumer ora passa all'agent
> i flag `--ollama-url/--ollama-model/--ollama-timeout`: un'immagine dell'agent costruita
> prima di questo cambiamento non li conosce e l'agent non parte. Dopo aver aggiornato il
> repository, la prima run di **qualsiasi** test (eco, latency, consumerchoice) va fatta
> senza `--skip-build`.

### Quanto dura

La durata è dominata dalle due fasi, più un overhead di deploy che cresce con il
numero di agenti (misurato: ~4 min con 7 provider, ~5 min con 17, ~12 min con 70).

Con `timer: 1h` e 100 agenti sono circa **2h20**.

---

## 4. La config: cosa toccare

Nel caso normale modifichi **solo** un file in `tests/configs/`. Ogni chiave
omessa prende un default sensato.

### Topologia

```yaml
consumers: 30
providers: 70
```

Sono cluster Kind veri: il totale è vincolato da RAM e CPU della macchina, non
dal codice. 100 agenti girano su un portatile robusto, ma il deploy si allunga.

Puoi anche fissare le regioni dei provider con `providerRegions:` (una per
provider); se ometti la chiave vengono pescate a caso da un elenco di 50 regioni
reali.

### Parametri dell'esperimento

```yaml
experiment:
  mode: reserve            # "reserve" = prenota davvero (peering Liqo)
                           # "observe" = guarda solo chi vincerebbe, non prenota
  duration: time           # "time" = a tempo | "iterations" = a numero di giri
  timer: 1h                # durata di OGNI fase (solo con duration: time)
  iterations: 15           # numero di giri per fase (solo con duration: iterations)
  phasePause: 35s          # pausa tra un'iterazione e la successiva
  policyPropagationWait: 35s
  advertisementLag: 35s
```

`duration: time` è la modalità da usare per i grafici: ogni consumer gira in modo
indipendente per il tempo stabilito, invece di aspettare gli altri a ogni giro.
Con questa modalità il campo `iterations` viene **ignorato** (resta in config
solo come ripiego).

### Parametri eco

```yaml
  carbonRefreshInterval: 2m   # ogni quanto cambiano le condizioni ambientali
  ecoCacheTTL: 5s             # quanto il provider tiene in cache il valore
  carbonLow: 50               # centro dell'intervallo "verde"
  carbonHigh: 800             # centro dell'intervallo "sporco"
  carbonGreenFractionMin: 0.3 # frazione minima di regioni verdi a ogni giro
  carbonGreenFractionMax: 0.7 # frazione massima
```

A ogni giro l'harness sorteggia quale frazione di regioni è verde e assegna a
ciascuna un valore attorno a `carbonLow` o `carbonHigh`, con un jitter di ±30 %.

### Parametri latency

```yaml
  latencyRefreshInterval: 2m
  latencyMinMs: 30
  latencyMaxMs: 250
```

Il ritardo simulato viene ridisegnato per ogni coppia (consumer, provider) a ogni
giro. **Non alzare `latencyMaxMs` sopra 250**: il prober ha una scadenza di 300 ms
per sonda, e un provider più lento risulterebbe irraggiungibile e sparirebbe dai
CSV invece di comparire come "lontano".

### Valori consigliati

| Chiave | Valore | Perché |
|---|---|---|
| `duration` | `time` | I grafici ragionano sul tempo trascorso |
| `timer` | `1h` | Con tick da 2 min dà ~30 gradini per fase. Con `30m` ne dà 15 e il grafico esce squadrato |
| `carbonRefreshInterval` | `2m` | Il minimo che tenga il ritardo di osservazione sotto un terzo del tick |
| `ecoCacheTTL` | `5s` | Qualsiasi valore sotto i 30 s è equivalente; **non legarlo al refresh interval** |
| `latencyRefreshInterval` | `2m` | Simmetria con l'eco; il vincolo qui è più lasco |
| `phasePause` | `35s` | Sotto i 30 s si campiona più in fretta di quanto l'ambiente cambi |
| `latencyMaxMs` | `250` | Tetto imposto dalla scadenza del prober |

---

## 5. Cambiare i chunk disponibili per provider

Questa è l'unica modifica che richiede di toccare il codice Go, ed è la manopola
più efficace per rendere il test interessante.

In `testlib/experiment.go`, nel blocco `=== CAP PROVIDER CAPACITY ===`:

```go
if err := SetProviderCapacity(ctx, spec.Kubeconfig, "4000m", "8Gi"); err != nil {
```

**Un chunk standard è 2 CPU / 4 GiB.** Quindi:

| Valore | Chunk per provider | Consumer che ci stanno |
|---|---|---|
| `"2000m", "4Gi"` | 1 | 1 |
| `"4000m", "8Gi"` | 2 (default) | 2 |
| `"8000m", "16Gi"` | 4 | 4 |

**Perché conta.** Senza questo tetto, ogni provider annuncerebbe la capacità reale
del nodo Kind — cioè tutta la CPU e la RAM della macchina host — e ci starebbero
dentro tutti i consumer contemporaneamente. Il provider più verde non si
saturerebbe mai e tutti finirebbero sempre lì: un artefatto dell'ambiente di test,
non una proprietà della policy.

Con il tetto basso, invece, quando il provider migliore si riempie la policy è
**costretta** a passare al secondo migliore — che è esattamente il comportamento
che il test deve misurare.

**Configurazioni tipiche.** Con 1 chunk per provider e 3 consumer / 7 provider,
ogni consumer occupa un provider intero e i tre si contendono i più verdi: la
competizione è massima e le differenze tra le policy emergono nettissime. È la
configurazione da usare per mostrare l'effetto nel modo più leggibile.

Alzando il numero di consumer a parità di chunk aumenti la pressione; alzando i
chunk la riduci. Come regola: **consumer ≈ provider × chunk** significa
federazione satura, e la policy passa il tempo a cercare posto. Molto meno significa
federazione vuota, e ogni policy trova sempre il suo preferito libero.

---

## 6. Cosa produce una run

I file finiscono in `results/<tipo-test>/<timestamp UTC>/`, per esempio
`results/comparative-eco/20260912T015958Z/`.

| File | Contenuto |
|---|---|
| `summary.md` / `summary.json` | Riepilogo leggibile: durate, numero di iterazioni, distribuzione delle scelte, metrica media per fase |
| `reservations.csv` | Una riga per prenotazione: chi, dove, quando, esito, metrica e carbon intensity del provider scelto |
| `selections.csv` | Una riga per decisione di placement, anche quando non porta a una prenotazione |
| `nodegroups.csv` | **Il file più ricco**: una riga per ogni volta che un consumer ha guardato un provider, con il valore che vedeva in quel momento. È la base per verificare l'allineamento |
| `federation.csv` | Fotografia dell'intera federazione a cadenza fissa (1 min), indipendente dal ritmo delle iterazioni |
| `probes.csv` | Solo nel test latency: gli RTT misurati |

Il campo `phase` (`phase-a` / `phase-b`) è presente ovunque ed è la chiave con cui
si separano le due fasi in analisi.

---

## 7. Verificare che la run sia valida

**Fallo prima di guardare i grafici.** Una run può completarsi senza errori e
comunque non essere utilizzabile per il confronto, se le due fasi non hanno visto
lo stesso ambiente.

```bash
python tests/scripts/verifyReplayAlignment.py --input results/.../nodegroups.csv
```

Lo script raggruppa le osservazioni per giro, confronta cosa vedeva ogni provider
nella Phase A contro la Phase B allo stesso tempo trascorso, e stampa la
percentuale di corrispondenza insieme a una **baseline di caso** (ottenuta
rimescolando le etichette dei provider). La baseline serve: da sola una
percentuale non è leggibile, perché valori simili capitano anche per fortuna.

| Esito | Soglia | Codice di uscita | Cosa fare |
|---|---|---|---|
| PASS | ≥ 80 % | 0 | Procedi |
| INCONCLUSIVE | 50–80 % | 2 | Di solito troppi pochi giri. Guarda il rapporto con la baseline |
| FAIL | < 50 % | 1 | Non sovrapporre i grafici. Controlla il ritardo di osservazione |

Se hai cambiato `carbonRefreshInterval`, passa anche `--tick-seconds` con il nuovo
valore in secondi, altrimenti lo script raggruppa con la finestra sbagliata.

### Per il test latency è un altro script

```bash
python3 tests/scripts/verifyLatencyReplayAlignment.py --input results/.../probes.csv
```

Legge `probes.csv`, non `nodegroups.csv`, e ragiona diversamente per due motivi.

Il primo: qui il valore è un **RTT misurato**, non un numero esatto letto da un'API.
Il confronto usa quindi una tolleranza (`--tolerance-ms`, default 10). La domanda a
cui risponde è "le due fasi hanno visto condizioni comparabili?", non "coincidono al
millisecondo": 10ms contro 13ms va benissimo, 10ms contro 400ms no. Sui dati reali la
distinzione è netta — letture della stessa condizione stanno a ~0.1ms l'una dall'altra,
estrazioni diverse a decine o centinaia di ms — quindi il valore esatto della
tolleranza non è critico.

Il secondo: la **Phase A del latency è strutturalmente rada**. Sotto Random il broker
maschera su un solo provider, quindi una coppia (consumer, provider) viene misurata solo
quando il caso la pesca — su una run 3×7 da un'ora sono state 7 finestre su 30, contro
28 su 30 della Phase B. Per questo l'output riporta una riga **Coverage**: leggi sempre
la percentuale insieme al numero di finestre confrontabili, perché una percentuale alta
su pochissime celle vale poco.

Nell'output trovi anche:

- **Estimated refresh grid** — lo script non assume dove cadano i confini dei tick, li
  ricava dai cambi di valore osservati (tutte le coppie cambiano insieme, su un solo
  ticker). Se una fase è troppo rada per individuarli da sola prende in prestito la
  stima dell'altra, e lo dichiara. Se compare un warning sull'affidabilità della griglia,
  il resto del risultato non è interpretabile.
- **allowing +/-1 window** — quanta parte degli scarti è solo incertezza sul confine
  della finestra invece che un ambiente diverso. Se questo numero è molto più alto dello
  strict, il problema è di posizionamento, non di condizioni.

Se il verdetto è FAIL o un INCONCLUSIVE ostinato, guarda i valori grezzi prima di
concludere qualcosa:

```bash
python3 tests/scripts/dumpLatencySequence.py --input results/.../probes.csv \
  --consumer consumer-1 --provider provider-3
```

Stampa le due fasi affiancate finestra per finestra. In pochi secondi vedi se le colonne
si somigliano (replay a posto, il punteggio sta misurando la scarsità dei campioni) o se
sono numeri scorrelati (allora è il replay da guardare).

### Grafici

Un grafico per test, costruito nello stesso modo: **sovrapposizione diretta**, con
Random (rosso) e la policy sotto test (verde) sullo stesso asse X, ciascuna misurata
dall'inizio della propria fase.

```bash
python tests/scripts/ecoDiagramMaker.py     --input results/comparative-eco/<timestamp>/reservations.csv
python tests/scripts/latencyDiagramMaker.py --input results/comparative-latency/<timestamp>/reservations.csv
```

- `ecoDiagramMaker.py` — asse Y: **somma** delle carbon intensity dei provider scelti.
  Output `carbon_intensity_comparison.*` e `carbon_summary.md`.
- `latencyDiagramMaker.py` — asse Y: **RTT medio** verso il provider scelto, tra i
  consumer attivi. Output `latency_comparison.*` e `latency_summary.md`.

Gli assi Y differiscono di proposito: una somma di millisecondi non ha significato
fisico e crescerebbe con il numero di consumer, mentre la media resta in ms reali e
sulla stessa scala da 3×7 a 30×70.

Entrambi leggono solo `reservations.csv`, accettano `--input` e `--output-dir`
(default: una cartella `analysis/` accanto al CSV) e scrivono PNG a 300 DPI, PDF, CSV
e un riepilogo in markdown. Se passi il CSV sbagliato escono con un messaggio che
indica lo script giusto. Servono `pandas` e `matplotlib`; gli script di verifica
invece usano solo la libreria standard.

---

## 8. Test veloce prima di una run lunga

Prima di impegnare due ore, conviene una run breve che usa **la stessa cadenza
reale** ma fasi da 6 minuti e topologia ridotta:

```bash
go run ./tests/comparative-eco/ --config tests/configs/eco-smoke.yaml
```

~22 minuti. Serve a verificare che l'allineamento ci sia, non a trarre conclusioni
sulla policy: con 3 soli giri per fase il verdetto sarà spesso `INCONCLUSIVE`, e in
quel caso quello che conta è il **rapporto con la baseline di caso**, non la
percentuale assoluta.

---

## 9. Quando qualcosa va storto

**La run si ferma con "phase A leaked capacity".** Durante la Phase A un rilascio
è fallito e il broker considera ancora occupato quel posto. La Phase B girerebbe
su una federazione più piccola di quella della Phase A, quindi l'harness si ferma
invece di produrre un confronto non valido. Il messaggio dice quali provider sono
coinvolti. Rilancia la run.

**Warning all'avvio sul ritardo di osservazione.** La config mette il ritardo sopra
metà dell'intervallo di refresh: i grafici non saranno sovrapponibili. Abbassa
`ecoCacheTTL` o alza `carbonRefreshInterval`.

**Cluster rimasti in giro dopo un'interruzione.** Se hai fermato la run con Ctrl-C
o hai usato `--keep-clusters`, i cluster Kind restano. Elencali con `kind get
clusters` e cancellali con `kind delete cluster --name <nome>`.

**Il deploy va in timeout.** Alza `infra.readinessTimeout` (default 10 min). Su una
macchina carica o con molti agenti può volerci di più.

**Il build delle immagini fallisce in `go mod download`** con un errore di DNS
(`lookup proxy.golang.org … i/o timeout`). Su alcuni server i container Docker non
risolvono i nomi, mentre l'host sì. Di solito non te ne accorgi, perché il build riusa i
moduli già scaricati; il problema compare quando cambia `go.mod`. Rilancia costruendo con
la rete dell'host:

```bash
DOCKER_BUILD_FLAGS=--network=host go run ./tests/comparative-eco/ --config tests/configs/eco-test.yaml
```

Vale per tutti i test, consumerchoice compreso. Per lo stesso motivo non lanciare
`go mod tidy` senza un motivo reale: anche solo riordinare `go.mod` invalida quella cache.

---

## 10. Cose che è utile sapere prima di interpretare i risultati

**Le due fasi eseguono un numero diverso di iterazioni.** È normale, non è un bug.
In Phase A la policy Random cambia provider quasi a ogni giro, e ogni cambio
comporta un rilascio più un nuovo peering — decine di secondi. In Phase B la
policy resta spesso dov'è, quindi il giro si chiude in pochi millisecondi e ne
entrano di più nello stesso tempo. Gli script di grafico ne tengono conto:
ricampionano su una griglia temporale regolare, quindi mediano nel tempo e non per
riga. **Le medie per riga nel `summary.md` no**: quelle sono pesate diversamente
nelle due fasi, quindi non usarle per il confronto finale.

**L'allineamento è buono, non perfetto.** Con il rapporto a 0.29 resta circa un
10 % di punti in cui le due fasi cadono su giri diversi. Vicino ai confini tra un
giro e l'altro le due curve possono discostarsi: è atteso.

**Il numero finale si muove tra una run e l'altra.** Con ~30 condizioni per fase,
la media della fase Random è stimata su un campione limitato. L'ordine di grandezza
del miglioramento è stabile; la cifra esatta no.

---

## 11. Il test ConsumerChoice

`tests/consumerchoice/` non è un confronto a due fasi: verifica che la policy
ConsumerChoice funzioni da capo a fondo. La Broker passa al consumer **tutti** i
provider idonei, un LLM locale ne sceglie uno in base a una richiesta in linguaggio
naturale, e quella scelta diventa una prenotazione vera che arriva a `Peered`.

Per ogni scenario il test fa due cose: le **decisioni registrate** (l'harness chiama il
selettore dell'agent e salva tutto: prompt, risposta, validazione) e il **percorso
dell'agent** (una prenotazione manuale dalla console, che l'agent del consumer decide da
solo chiedendo al modello). Il modello viene interrogato esattamente come lo interroga
l'agent: JSON semplice, parametri di default di Ollama, stesso timeout.

Anche qui è tutto automatico, **Ollama compreso**: il test avvia il suo container,
scarica il modello la prima volta (poi resta in cache) e lo rimuove alla fine.

```bash
./tests/consumerchoice/run-consumerchoice.sh --config tests/consumerchoice/configs/default.yaml
```

Nello YAML modifichi di solito solo `scenarios[].userRequest`. Scenari, criteri di
valutazione, metriche e file prodotti sono descritti in `tests/consumerchoice/README.md`.

Due differenze pratiche rispetto a eco e latency:

- la topologia standard è **1 consumer e 9 provider**, con profili fissi (carbonio,
  prezzo, capacità, regione) scelti apposta perché nessun provider vinca su tutto;
- la prima run dopo aver toccato codice della Broker o dell'agent **non** va lanciata
  con `--skip-build`.
