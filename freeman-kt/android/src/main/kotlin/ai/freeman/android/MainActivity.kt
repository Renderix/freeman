package ai.freeman.android

import android.app.Activity
import android.os.Bundle
import android.widget.TextView

class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val label = TextView(this).apply { text = "Freeman starting..." }
        setContentView(label)
        // Platform wiring added in platform-wiring plan
    }
}
