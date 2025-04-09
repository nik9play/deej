/*
  This sketch sends values only when the slider is moved and also does noise reduction.
  You can customize the degree of reduction by changing the THRESHOLD constant.
  When using this sketch, set the noise_reduction parameter in the deej config to none or low.
*/

#define THRESHOLD 5
#define NUM_SLIDERS 5

const int analogInputs[NUM_SLIDERS] = { A0, A1, A2, A3, A4 };
int analogSliderValues[NUM_SLIDERS];

void setup() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    pinMode(analogInputs[i], INPUT);
    analogSliderValues[i] = -1023;
  }

  Serial.begin(9600);

  // arduino resets when reconnected to the serial port, 
  // so deej should get the initial slider values with no problem
  updateSliderValues();
  
  // print slider values several times to trigger slider change in deej
  sendSliderValues();
  sendSliderValues();
}

void loop() {
  updateSliderValues();
}

void updateSliderValues() {
  bool updated = false;

  for (int i = 0; i < NUM_SLIDERS; i++) {
    int new_value = analogRead(analogInputs[i]);

    if (abs(analogSliderValues[i] - new_value) >= THRESHOLD) {
      analogSliderValues[i] = new_value;
      updated = true;
    }
  }

  if (updated) {
    sendSliderValues();
  }
}

void sendSliderValues() {
  String builtString = String("");

  for (int i = 0; i < NUM_SLIDERS; i++) {
    builtString += String(1023 - analogSliderValues[i]);

    if (i < NUM_SLIDERS - 1) {
      builtString += String("|");
    }
  }

  Serial.println(builtString);
}
