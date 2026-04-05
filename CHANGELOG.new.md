## fix
Replace slot channel semaphore with AcquireSlot (inFlight < cwnd check under mutex)
Eliminates slot inflation/starvation race between OnAck and OnLoss
