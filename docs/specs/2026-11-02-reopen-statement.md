# Reopen a closed card statement

`POST /v1.0/console/finance/cards/:id/reopen {"month":"YYYY-MM"}` (write verb, idempotent).

- Only the card's **latest closed** statement (`open_month - 1`) of an **active** card, while its bill is **unpaid**. Otherwise 409 `statement_not_reopenable`.
- The statement bill is canceled **without a ledger posting**: a statement bill is never recognised (the purchases already hit the DRE), so there is nothing to reverse. A negative statement's carried credit row is deleted.
- The frozen statement row is deleted and `open_month` goes back to the month. Purchases and corrections land on it again.
- The close schedule is pushed `reopenGraceDays` (7) ahead: the job closes it again after that, or the user closes it sooner with "Fechar fatura agora". Editing the card's closing day ends the grace.
- Closing again makes a **new** bill (id includes the card `version`); the canceled one stays in history.
- A statement bill of an **archived** card can be canceled from "A pagar" (same no-posting rule). Archived cards no longer close statements (`CloseDue`).

Cost: no new resources; one extra TransactWrite per reopen.
